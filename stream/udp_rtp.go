package stream

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/qist/tvgate/logger"
	"golang.org/x/net/ipv4"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ====================
// UDP RTP ->HTTP 流媒体客户端
// ====================

const (
	StateStoppeds   = 0
	StatePlayings   = 1
	StateErrors     = 2
	NULL_PID        = 0x1FFF // TS 空包 PID
	MAX_BUFFER_SIZE = 65536  // 缓存最大值

	// MPEG payload-type constants - adopted from VLC 0.8.6
	P_MPGA = 0x0E // MPEG audio
	P_MPGV = 0x20 // MPEG video

	// RTP constants
	RTP_VERSION = 2
	PAT_PID     = 0x0000
	PMT_PID     = 0x1000
)

type rtpSeqEntry struct {
	sequences  []uint16
	lastActive time.Time
}

const (
	rtpSequenceWindow = 200
	rtpSSRCExpire     = 30 * time.Second // 超过30秒未收到包就清理
)

// ====================
// RingBuffer 环形缓冲区
// ====================
type RingBuffer struct {
	buf   [][]byte
	size  int
	start int
	count int
	lock  sync.Mutex
}

func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{
		buf:  make([][]byte, size),
		size: size,
	}
}

func (r *RingBuffer) Push(item []byte) {
	r.lock.Lock()
	defer r.lock.Unlock()
	if r.count < r.size {
		r.buf[(r.start+r.count)%r.size] = item
		r.count++
	} else {
		r.buf[r.start] = item
		r.start = (r.start + 1) % r.size
	}
}

func (r *RingBuffer) GetAll() [][]byte {
	r.lock.Lock()
	defer r.lock.Unlock()
	out := make([][]byte, r.count)
	for i := 0; i < r.count; i++ {
		out[i] = r.buf[(r.start+i)%r.size]
	}
	return out
}

// ====================
// 客户端结构
// ====================
type hubClient struct {
	ch     chan []byte
	connID string
}

// ====================
// StreamHub 流转发核心
// ====================
type StreamHub struct {
	Mu          sync.RWMutex
	Clients     map[string]hubClient // key = connID
	AddCh       chan hubClient
	RemoveCh    chan string
	UdpConns    []*net.UDPConn
	Closed      chan struct{}
	BufPool     *sync.Pool
	LastFrame   []byte
	CacheBuffer *RingBuffer
	AddrList    []string
	PacketCount uint64
	DropCount   uint64
	state       int // 0: stopped, 1: playing, 2: error
	stateCond   *sync.Cond
	OnEmpty     func(h *StreamHub) // 当客户端数量为0时触发
	// lastPAT     []byte
	// lastPMT     []byte
	// patSent     bool
	// lastRTPSequence uint16
	// lastRTPSSRC     uint32
	rtpBuffer      []byte // RTP拼接缓存
	lastCCMap      map[int]byte
	rtpSequenceMap map[uint32]*rtpSeqEntry
}

// ====================
// 创建新 Hub
// ====================
func NewStreamHub(addrs []string, ifaces []string) (*StreamHub, error) {
	if len(addrs) == 0 {
		return nil, fmt.Errorf("至少一个 UDP 地址")
	}

	hub := &StreamHub{
		Clients:     make(map[string]hubClient),
		AddCh:       make(chan hubClient, 1024),
		RemoveCh:    make(chan string, 1024),
		UdpConns:    make([]*net.UDPConn, 0, len(addrs)),
		CacheBuffer: NewRingBuffer(8192), // 默认缓存8192帧
		Closed:      make(chan struct{}),
		BufPool:     &sync.Pool{New: func() any { return make([]byte, 64*1024) }},
		AddrList:    addrs,
		state:       StatePlayings,
		lastCCMap:   make(map[int]byte),
	}
	hub.stateCond = sync.NewCond(&hub.Mu)

	var lastErr error
	for _, addr := range addrs {
		udpAddr, err := net.ResolveUDPAddr("udp", addr)
		if err != nil {
			lastErr = err
			continue
		}

		if len(ifaces) == 0 {
			conn, err := listenMulticast(udpAddr, nil)
			if err != nil {
				lastErr = err
				continue
			}
			hub.UdpConns = append(hub.UdpConns, conn)
		} else {
			for _, name := range ifaces {
				iface, ierr := net.InterfaceByName(name)
				if ierr != nil {
					lastErr = ierr
					continue
				}
				conn, err := listenMulticast(udpAddr, []*net.Interface{iface})
				if err == nil {
					hub.UdpConns = append(hub.UdpConns, conn)
					break
				}
				lastErr = err
			}
		}
	}

	if len(hub.UdpConns) == 0 {
		return nil, fmt.Errorf("所有网卡监听失败: %v", lastErr)
	}

	go hub.run()
	hub.startReadLoops()
	return hub, nil
}

// ====================
// 多播监听封装
// ====================
func listenMulticast(addr *net.UDPAddr, ifaces []*net.Interface) (*net.UDPConn, error) {
	if addr == nil || addr.IP == nil || !isMulticast(addr.IP) {
		return nil, fmt.Errorf("仅支持多播地址: %v", addr)
	}

	var conn *net.UDPConn
	var lastErr error
	var err error

	if len(ifaces) == 0 {
		conn, err = net.ListenMulticastUDP("udp", nil, addr)
		if err != nil {
			logger.LogPrintf("⚠️ 多播监听失败，尝试回退单播: %v", err)
			conn, err = net.ListenUDP("udp", addr)
			if err != nil {
				return nil, fmt.Errorf("默认接口监听失败: %w", err)
			}
			logger.LogPrintf("🟡 已回退为单播 UDP 监听 %v", addr)
		} else {
			logger.LogPrintf("🟢 监听 %v (全部接口)", addr)
		}
	} else {
		for _, iface := range ifaces {
			if iface == nil {
				continue
			}
			conn, err = net.ListenMulticastUDP("udp", iface, addr)
			if err == nil {
				logger.LogPrintf("🟢 监听 %v@%s 成功", addr, iface.Name)
				break
			}
			lastErr = err
			logger.LogPrintf("⚠️ 监听 %v@%s 失败: %v", addr, iface.Name, err)
		}

		if conn == nil {
			conn, err = net.ListenUDP("udp", addr)
			if err != nil {
				return nil, fmt.Errorf("所有网卡监听失败且单播监听失败: %v (last=%v)", err, lastErr)
			}
			logger.LogPrintf("🟡 所有网卡多播失败，已回退为单播 UDP 监听 %v", addr)
		}
	}
	_ = conn.SetReadBuffer(16 * 1024 * 1024)

	return conn, nil
}

func isMulticast(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	return ip4[0] >= 224 && ip4[0] <= 239
}

// ====================
// 启动 UDPConn readLoop
// ====================
func (h *StreamHub) startReadLoops() {
	for idx, conn := range h.UdpConns {
		hubAddr := h.AddrList[idx%len(h.AddrList)]
		go h.readLoop(conn, hubAddr)
	}
}

func (h *StreamHub) readLoop(conn *net.UDPConn, hubAddr string) {
	if conn == nil {
		return
	}

	udpAddr, _ := net.ResolveUDPAddr("udp", hubAddr)
	dstIP := udpAddr.IP.String()
	pconn := ipv4.NewPacketConn(conn)
	_ = pconn.SetControlMessage(ipv4.FlagDst, true)

	for {
		select {
		case <-h.Closed:
			return
		default:
		}

		buf := h.BufPool.Get().([]byte)
		n, cm, _, err := pconn.ReadFrom(buf)
		if err != nil {
			h.BufPool.Put(buf)
			if !errors.Is(err, net.ErrClosed) {
			}
			return
		}

		if cm != nil && cm.Dst.String() != dstIP {
			h.BufPool.Put(buf)
			continue
		}

		data := make([]byte, n)
		copy(data, buf[:n])
		h.BufPool.Put(buf)

		h.Mu.RLock()
		closed := h.state == StateStoppeds || h.CacheBuffer == nil
		h.Mu.RUnlock()
		if closed {
			return
		}

		// 处理RTP包，提取有效载荷
		processedData := h.processRTPPacket(data)

		// 广播，不进行任何视频分析
		h.broadcast(processedData)
	}
}

// ====================
// RTP处理相关函数
// ====================

// hexdumpPreview 返回前 n 个字节的十六进制预览
func hexdumpPreview(buf []byte, n int) string {
	if len(buf) > n {
		buf = buf[:n]
	}
	return hex.EncodeToString(buf)
}

func (h *StreamHub) cleanupOldSSRCs() {
	now := time.Now()
	for ssrc, entry := range h.rtpSequenceMap {
		if now.Sub(entry.lastActive) > rtpSSRCExpire {
			delete(h.rtpSequenceMap, ssrc)
		}
	}
}

// rtpPayloadGet 从RTP包中提取有效载荷位置和大小
func rtpPayloadGet(buf []byte) (startOff, endOff int, err error) {
	if len(buf) < 12 {
		return 0, 0, errors.New("buffer too small")
	}

	// RTP版本检查
	version := (buf[0] >> 6) & 0x03
	if version != RTP_VERSION {
		return 0, 0, fmt.Errorf("invalid RTP version=%d", version)
	}

	// 计算头部大小
	cc := buf[0] & 0x0F
	startOff = 12 + (4 * int(cc))

	// 检查扩展头
	x := (buf[0] >> 4) & 0x01
	if x == 1 { // 扩展头存在
		if startOff+4 > len(buf) {
			return 0, 0, errors.New("buffer too small for extension header")
		}
		extLen := int(binary.BigEndian.Uint16(buf[startOff+2 : startOff+4]))
		startOff += 4 + (4 * extLen)
	}

	// 检查填充
	p := (buf[0] >> 5) & 0x01
	if p == 1 { // 填充存在
		if len(buf) > 0 {
			endOff = int(buf[len(buf)-1])
		}
	}

	if startOff+endOff > len(buf) {
		return 0, 0, errors.New("invalid RTP packet structure")
	}

	// 保留兜底逻辑（不打印日志）
	payloadLen := len(buf) - startOff - endOff
	if payloadLen > 0 {
		if buf[startOff] != 0x47 || payloadLen%188 != 0 {
			// 只是检查，不做打印
		}
	}

	return startOff, endOff, nil
}

func makeNullTS() []byte {
	ts := make([]byte, 188)
	ts[0] = 0x47 // sync byte
	ts[1] = 0x1F // PID high (0x1FFF = null PID)
	ts[2] = 0xFF // PID low
	ts[3] = 0x10 // payload unit start, adaptation field exists, CC=0

	// 适配字段
	ts[4] = 0x07 // 适配字段长度: 188-5-4=179 bytes
	ts[5] = 0x00 // 适配字段标志: 无特殊标志

	// 填充剩余字节为 0xFF
	for i := 6; i < 188; i++ {
		ts[i] = 0xFF
	}
	return ts
}

// 改进的 processRTPPacket 函数
func (h *StreamHub) processRTPPacket(data []byte) []byte {
	// 已经是完整 TS 包直接返回（兼容非 RTP 流）
	if len(data) >= 188 && data[0] == 0x47 {
		return data
	}

	// RTP Header 最小长度检查
	if len(data) < 12 {
		return data
	}

	version := (data[0] >> 6) & 0x03
	if version != RTP_VERSION {
		return data
	}

	sequence := binary.BigEndian.Uint16(data[2:4])
	ssrc := binary.BigEndian.Uint32(data[8:12])

	if h.rtpSequenceMap == nil {
		h.rtpSequenceMap = make(map[uint32]*rtpSeqEntry)
	}

	entry, ok := h.rtpSequenceMap[ssrc]
	if !ok {
		entry = &rtpSeqEntry{}
		h.rtpSequenceMap[ssrc] = entry
	}

	// 去重检查
	duplicate := false
	for _, seq := range entry.sequences {
		if seq == sequence {
			duplicate = true
			break
		}
	}
	if duplicate {
		return nil
	}

	entry.sequences = append(entry.sequences, sequence)
	if len(entry.sequences) > rtpSequenceWindow {
		entry.sequences = entry.sequences[len(entry.sequences)-rtpSequenceWindow:]
	}
	entry.lastActive = time.Now()

	h.cleanupOldSSRCs()

	// 提取 RTP Payload
	startOff, endOff, err := rtpPayloadGet(data)
	if err != nil || startOff >= len(data)-endOff {
		return data // ✅ 兜底逻辑，返回原始数据
	}

	payloadType := data[1] & 0x7F
	if payloadType == P_MPGA || payloadType == P_MPGV {
		if startOff+4 < len(data)-endOff {
			startOff += 4
		}
	}

	payload := data[startOff : len(data)-endOff]

	// ✅ 兜底检查，必须对齐 188
	if len(payload) < 188 || payload[0] != 0x47 || len(payload)%188 != 0 {
		return data
	}

	// 拼接缓存，处理分片
	h.rtpBuffer = append(h.rtpBuffer, payload...)
	if len(h.rtpBuffer) < 188 {
		return nil
	}

	if h.rtpBuffer[0] != 0x47 {
		idx := bytes.IndexByte(h.rtpBuffer, 0x47)
		if idx < 0 {
			h.rtpBuffer = nil
			return nil
		}
		h.rtpBuffer = h.rtpBuffer[idx:]
		if len(h.rtpBuffer) < 188 {
			return nil
		}
	}

	alignedSize := (len(h.rtpBuffer) / 188) * 188
	chunk := h.rtpBuffer[:alignedSize]
	if alignedSize < len(h.rtpBuffer) {
		h.rtpBuffer = append([]byte{}, h.rtpBuffer[alignedSize:]...)
	} else {
		h.rtpBuffer = nil
	}

	out := make([]byte, 0, alignedSize)
	for i := 0; i < len(chunk); i += 188 {
		ts := chunk[i : i+188]
		if ts[0] != 0x47 {
			continue
		}

		pid := ((int(ts[1]) & 0x1F) << 8) | int(ts[2])
		tsCC := ts[3] & 0x0F

		if pid != NULL_PID {
			if last, ok := h.lastCCMap[pid]; ok {
				diff := (int(tsCC) - int(last) + 16) & 0x0F
				if diff > 1 {
					for j := 1; j < diff; j++ {
						out = append(out, makeNullTS()...)
					}
				}
			}
			h.lastCCMap[pid] = tsCC
		}

		out = append(out, ts...)
	}

	return out
}

// ====================
// 广播到所有客户端
// ====================
func (h *StreamHub) broadcast(data []byte) {
	var clients map[string]hubClient

	h.Mu.Lock()
	if h.Closed == nil || h.CacheBuffer == nil || h.Clients == nil {
		h.Mu.Unlock()
		return
	}

	// 更新状态
	h.PacketCount++
	h.LastFrame = data
	h.CacheBuffer.Push(data)

	// 播放状态更新
	if h.state != StatePlayings {
		h.state = StatePlayings
		h.stateCond.Broadcast()
	}

	// 拷贝客户端 map，解锁后发送
	clients = make(map[string]hubClient, len(h.Clients))
	for k, v := range h.Clients {
		clients[k] = v
	}
	h.Mu.Unlock()

	// 非阻塞广播
	for _, client := range clients {
		select {
		case client.ch <- data:
		default:
			h.Mu.Lock()
			h.DropCount++
			if h.DropCount%100 == 0 {
				select {
				case <-client.ch:
				default:
				}
				if h.LastFrame != nil {
					select {
					case client.ch <- h.LastFrame:
					default:
					}
				}
			}
			h.Mu.Unlock()
		}
	}
}

// ====================
// 客户端管理循环
// ====================
func (h *StreamHub) run() {
	for {
		select {
		case client := <-h.AddCh:
			h.Mu.Lock()
			h.Clients[client.connID] = client
			curCount := len(h.Clients)
			h.Mu.Unlock()
			go h.sendInitial(client.ch)
			logger.LogPrintf("➕ 客户端加入，当前客户端数量=%d", curCount)

		case connID := <-h.RemoveCh:
			h.Mu.Lock()
			if client, ok := h.Clients[connID]; ok {
				delete(h.Clients, connID)
				close(client.ch)
				curCount := len(h.Clients)
				logger.LogPrintf("➖ 客户端离开，当前客户端数量=%d", curCount)
			}
			// 如果没有客户端，清空累积缓存
			if len(h.Clients) == 0 {
				h.Mu.Unlock()
				h.Close()
				if h.OnEmpty != nil {
					h.OnEmpty(h) // 自动删除 hub
				}
				return
			}
			h.Mu.Unlock()

		case <-h.Closed:
			h.Mu.Lock()
			for _, client := range h.Clients {
				close(client.ch)
			}
			h.Clients = nil
			h.Mu.Unlock()
			return
		}
	}
}

// ====================
// 新客户端发送初始化帧
// ====================
func (h *StreamHub) sendInitial(ch chan []byte) {
	// 获取缓存快照，锁粒度最小化
	h.Mu.Lock()
	cachedFrames := h.CacheBuffer.GetAll()
	h.Mu.Unlock()

	go func() {
		// 发送所有缓存帧
		for _, f := range cachedFrames {
			// 检查 hub 是否已关闭
			select {
			case <-h.Closed:
				return
			default:
			}

			// 非阻塞发送
			select {
			case ch <- f:
			default:
				return
			}
		}
	}()
}

// ====================
// HTTP 播放
// ====================
func (h *StreamHub) ServeHTTP(w http.ResponseWriter, r *http.Request, contentType string, updateActive func()) {
	select {
	case <-h.Closed:
		http.Error(w, "Stream hub closed", http.StatusServiceUnavailable)
		return
	default:
	}

	connID := r.Header.Get("X-ConnID")
	if connID == "" {
		connID = strconv.FormatInt(time.Now().UnixNano(), 10)
	}

	// 增加缓冲区大小
	ch := make(chan []byte, 4096)
	h.AddCh <- hubClient{ch: ch, connID: connID}
	defer func() { h.RemoveCh <- connID }()
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("ContentFeatures.DLNA.ORG", "DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=01700000000000000000000000000000")
	w.Header().Set("TransferMode.DLNA.ORG", "Streaming")
	w.Header().Set("Content-Type", contentType)

	userAgent := r.Header.Get("User-Agent")
	switch {
	case strings.Contains(userAgent, "VLC"):
		w.Header().Del("Transfer-Encoding")
		w.Header().Set("Accept-Ranges", "none")
	default:
		w.Header().Set("Transfer-Encoding", "chunked")
		w.Header().Set("Accept-Ranges", "none")
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	bufferedBytes := 0
	const maxBufferSize = 128 * 1024 // 128KB缓冲区
	flushTicker := time.NewTicker(50 * time.Millisecond)
	defer flushTicker.Stop()
	activeTicker := time.NewTicker(5 * time.Second)
	defer activeTicker.Stop()

	if !h.WaitForPlaying(ctx) {
		return
	}

	for {
		select {
		case data, ok := <-ch:
			if !ok {
				return
			}
			n, err := w.Write(data)
			if err != nil {
				return
			}
			bufferedBytes += n
			if bufferedBytes >= maxBufferSize {
				flusher.Flush()
				bufferedBytes = 0
			}
		case <-flushTicker.C:
			if bufferedBytes > 0 {
				flusher.Flush()
				bufferedBytes = 0
			}
		case <-activeTicker.C:
			if updateActive != nil {
				updateActive()
			}
		case <-ctx.Done():
			return
		case <-h.Closed:
			return
		}
	}
}

// ====================
// 关闭 Hub
// ====================
func (h *StreamHub) Close() {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	select {
	case <-h.Closed:
		return // 已经关闭过
	default:
		close(h.Closed)
	}

	// 关闭 UDP 连接
	for _, conn := range h.UdpConns {
		if conn != nil {
			_ = conn.Close()
		}
	}
	h.UdpConns = nil

	// 关闭客户端 channel
	for id, client := range h.Clients {
		if client.ch != nil {
			close(client.ch)
		}
		delete(h.Clients, id)
	}
	h.Clients = nil

	// 清理缓存
	h.CacheBuffer = nil
	h.LastFrame = nil

	// 状态更新并广播
	h.state = StateStoppeds
	if h.stateCond != nil {
		h.stateCond.Broadcast()
	}

	logger.LogPrintf("UDP监听已关闭，端口已释放: %s", h.AddrList[0])
}

// ====================
// 判断 Hub 是否关闭
// ====================
func (h *StreamHub) IsClosed() bool {
	select {
	case <-h.Closed:
		return true
	default:
		return false
	}
}

// ====================
// 等待播放状态
// ====================
func (h *StreamHub) WaitForPlaying(ctx context.Context) bool {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	if h.IsClosed() || h.state == StateErrors {
		return false
	}
	if h.state == StatePlayings {
		return true
	}

	for h.state == StateStoppeds && !h.IsClosed() {
		done := make(chan struct{})
		go func() {
			defer close(done)
			h.stateCond.Wait()
		}()
		select {
		case <-done:
			if h.state == StateErrors {
				return false
			}
			if h.state == StatePlayings {
				return true
			}
		case <-ctx.Done():
			return false
		}
	}
	return !h.IsClosed() && h.state == StatePlayings
}

// ====================
// MultiChannelHub
// ====================
type MultiChannelHub struct {
	Mu   sync.RWMutex
	Hubs map[string]*StreamHub
}

var GlobalMultiChannelHub = NewMultiChannelHub()

func NewMultiChannelHub() *MultiChannelHub {
	return &MultiChannelHub{
		Hubs: make(map[string]*StreamHub),
	}
}

// MD5(IP:Port@ifaces) 作为 Hub key
func (m *MultiChannelHub) HubKey(udpAddr string, ifaces []string) string {
	// 将UDP地址和接口列表组合成唯一的键
	keyStr := udpAddr
	if len(ifaces) > 0 {
		keyStr += "@" + strings.Join(ifaces, ",")
	}
	h := md5.Sum([]byte(keyStr))
	return hex.EncodeToString(h[:])
}

func (m *MultiChannelHub) GetOrCreateHub(udpAddr string, ifaces []string) (*StreamHub, error) {
	key := m.HubKey(udpAddr, ifaces)

	m.Mu.RLock()
	hub, exists := m.Hubs[key]
	m.Mu.RUnlock()

	if exists && !hub.IsClosed() {
		return hub, nil
	}

	newHub, err := NewStreamHub([]string{udpAddr}, ifaces)
	if err != nil {
		return nil, err
	}

	// 当客户端为0时自动删除 hub
	newHub.OnEmpty = func(h *StreamHub) {
		GlobalMultiChannelHub.RemoveHubEx(h.AddrList[0], ifaces)
	}

	m.Mu.Lock()
	m.Hubs[key] = newHub
	m.Mu.Unlock()
	return newHub, nil
}

func (m *MultiChannelHub) RemoveHub(udpAddr string) {
	m.RemoveHubEx(udpAddr, nil)
}

func (m *MultiChannelHub) RemoveHubEx(udpAddr string, ifaces []string) {
	key := m.HubKey(udpAddr, ifaces)

	m.Mu.Lock()
	hub, ok := m.Hubs[key]
	if !ok {
		m.Mu.Unlock()
		return
	}

	// 先从 map 删除，避免 Close 时有 goroutine 再访问
	delete(m.Hubs, key)
	m.Mu.Unlock()

	// 安全关闭 hub
	hub.Close()
}

// ====================
// 更新 Hub 的接口
// ====================
func (h *StreamHub) UpdateInterfaces(ifaces []string) error {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	var newConns []*net.UDPConn
	var lastErr error

	for _, addr := range h.AddrList {
		udpAddr, err := net.ResolveUDPAddr("udp", addr)
		if err != nil {
			lastErr = err
			continue
		}

		var conn *net.UDPConn
		for _, name := range ifaces {
			iface, ierr := net.InterfaceByName(name)
			if ierr != nil {
				lastErr = ierr
				continue
			}
			conn, err = listenMulticast(udpAddr, []*net.Interface{iface})
			if err == nil {
				newConns = append(newConns, conn)
				break
			}
			lastErr = err
		}

		// 最后尝试默认接口
		if conn == nil {
			conn, err = listenMulticast(udpAddr, nil)
			if err != nil {
				lastErr = err
				continue
			}
			newConns = append(newConns, conn)
		}
	}

	if len(newConns) == 0 {
		return fmt.Errorf("所有网卡更新失败: %v", lastErr)
	}

	// 替换 UDPConns
	for _, conn := range h.UdpConns {
		_ = conn.Close()
	}
	h.UdpConns = newConns

	// 重新启动 readLoops
	h.startReadLoops()

	logger.LogPrintf("✅ Hub UDPConn 已更新 (仅接口)，网卡=%v", ifaces)

	return nil
}

// ====================
// 客户端迁移到新 Hub
// ====================
func (h *StreamHub) TransferClientsTo(newHub *StreamHub) {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	newHub.Mu.Lock()
	defer newHub.Mu.Unlock()

	if newHub.Clients == nil {
		newHub.Clients = make(map[string]hubClient)
	}
	if newHub.CacheBuffer == nil {
		newHub.CacheBuffer = NewRingBuffer(h.CacheBuffer.size)
	}

	// 迁移缓存数据
	for _, f := range h.CacheBuffer.GetAll() {
		newHub.CacheBuffer.Push(f)
	}

	// 迁移客户端
	for connID, client := range h.Clients {
		newHub.Clients[connID] = client

		// 发送最后关键帧序列
		for _, frame := range h.CacheBuffer.GetAll() {
			select {
			case client.ch <- frame:
			default:
			}
		}

		// 再发送最后一帧数据，保证客户端能立即播放
		if len(h.LastFrame) > 0 {
			select {
			case client.ch <- h.LastFrame:
			default:
			}
		}
	}

	h.Clients = make(map[string]hubClient)
	logger.LogPrintf("🔄 客户端已迁移到新Hub，数量=%d", len(newHub.Clients))
}
