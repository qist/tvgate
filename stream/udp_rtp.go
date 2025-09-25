package stream

import (
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
	"sync/atomic"
	// "sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TS包同步字节
const TS_SYNC_BYTE = 0x47
const TS_PACKET_SIZE = 188

// ====================
// RingBuffer
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
// StreamHub
// ====================
// const (
// 	StateStopped = 0
// 	StatePlaying = 1
// 	StateError   = 2
// )

type StreamHub struct {
	Mu             sync.Mutex
	Clients        map[string]hubClient // key = connID
	AddCh          chan hubClient
	RemoveCh       chan string
	UdpConns       []*net.UDPConn
	Closed         chan struct{}
	BufPool        *sync.Pool
	LastFrame      []byte
	LastKeyFrame   []byte
	LastInitFrame  [][]byte
	CacheBuffer    *RingBuffer
	DetectedFormat string
	AddrList       []string
	PacketCount    uint64
	DropCount      uint64
	hasSPS         bool
	hasPPS         bool
	state          int // 0: stopped, 1: playing, 2: error
	stateCond      *sync.Cond
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
		CacheBuffer: NewRingBuffer(4096),
		Closed:      make(chan struct{}),
		BufPool:     &sync.Pool{New: func() any { return make([]byte, 32*1024) }},
		AddrList:    addrs,
		state:       StatePlaying,
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
		logger.LogPrintf("🟢 Listening on %s via interfaces %v", udpAddr, ifaces)
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

	_ = conn.SetReadBuffer(8 * 1024 * 1024)
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
		logger.LogPrintf("❌ readLoop: conn is nil, hubAddr=%s", hubAddr)
		return
	}

	udpAddr, _ := net.ResolveUDPAddr("udp", hubAddr)
	dstIP := udpAddr.IP.String()
	buf := make([]byte, 32*1024)

	pconn := ipv4.NewPacketConn(conn)
	_ = pconn.SetControlMessage(ipv4.FlagDst, true)

	for {
		select {
		case <-h.Closed:
			logger.LogPrintf("ℹ️ readLoop: hub closed, hubAddr=%s", hubAddr)
			return
		default:
		}

		n, cm, _, err := pconn.ReadFrom(buf)
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				logger.LogPrintf("❌ ReadFrom failed: %v, hubAddr=%s", err, hubAddr)
			}
			return
		}

		if cm != nil && cm.Dst.String() != dstIP {
			// logger.LogPrintf("⚠️ 数据来源 IP 不匹配: dst=%s, expected=%s, hubAddr=%s, n=%d",
				// cm.Dst, dstIP, hubAddr, n)
			continue
		}

		data := make([]byte, n)
		copy(data, buf[:n])
		h.broadcast(data)
	}
}

// ====================
// 广播到所有客户端
// ====================
func (h *StreamHub) broadcast(data []byte) {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	h.PacketCount++
	h.LastFrame = data
	h.CacheBuffer.Push(data)

	if h.DetectedFormat == "" {
		h.DetectedFormat = detectStreamFormat(data)
	}

	if h.isKeyFrameByFormat(data, h.DetectedFormat) {
		h.LastKeyFrame = data
		h.LastInitFrame = append(h.LastInitFrame[:0], h.CacheBuffer.GetAll()...)
	}

	if h.state != StatePlaying {
		h.state = StatePlaying
		h.stateCond.Broadcast()
	}

	for _, client := range h.Clients {
		select {
		case client.ch <- data:
		default:
			h.DropCount++
		}
	}
}

// ====================
// 客户端管理循环
// ====================
func (h *StreamHub) run() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			GlobalMultiChannelHub.CheckIsolation()
		case client := <-h.AddCh:
			h.Mu.Lock()
			h.Clients[client.connID] = client
			h.Mu.Unlock()
			go h.sendInitial(client.ch)
		case connID := <-h.RemoveCh:
			h.Mu.Lock()
			if client, ok := h.Clients[connID]; ok {
				delete(h.Clients, connID)
				close(client.ch)
			}
			if len(h.Clients) == 0 {
				h.Mu.Unlock()
				h.Close()
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
	h.Mu.Lock()
	lastInit := append([][]byte(nil), h.LastInitFrame...)
	h.Mu.Unlock()

	if len(lastInit) > 0 {
		for _, f := range lastInit {
			select {
			case ch <- f:
			default:
			}
		}
		return
	}

	h.Mu.Lock()
	cache := h.CacheBuffer.GetAll()
	h.Mu.Unlock()
	var sentKey bool
	for _, f := range cache {
		if !sentKey && h.isKeyFrameByFormat(f, h.DetectedFormat) {
			sentKey = true
		}
		if sentKey {
			select {
			case ch <- f:
			default:
			}
		}
	}
}

// ====================
// HTTP 播放
// ====================
func (h *StreamHub) ServeHTTP(w http.ResponseWriter, r *http.Request, contentType string, updateActive func()) {
	hubName := strings.Join(h.AddrList, ",")
	logger.LogPrintf("DEBUG: Hub [%s] ServeHTTP 开始 - ClientIP: %s", hubName, r.RemoteAddr)

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

	ch := make(chan []byte, 1024)
	h.AddCh <- hubClient{ch: ch, connID: connID}
	defer func() { h.RemoveCh <- connID }()

	w.Header().Set("Content-Type", contentType)
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	bufferedBytes := 0
	flushTicker := time.NewTicker(200 * time.Millisecond)
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
		return
	default:
		close(h.Closed)
	}

	for _, conn := range h.UdpConns {
		_ = conn.Close()
	}
	h.UdpConns = nil

	for _, client := range h.Clients {
		close(client.ch)
	}
	h.Clients = nil
	h.CacheBuffer = nil

	h.state = StateStopped
	h.stateCond.Broadcast()
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

	if h.IsClosed() || h.state == StateError {
		return false
	}
	if h.state == StatePlaying {
		return true
	}

	for h.state == StateStopped && !h.IsClosed() {
		done := make(chan struct{})
		go func() {
			defer close(done)
			h.stateCond.Wait()
		}()
		select {
		case <-done:
			if h.state == StateError {
				return false
			}
			if h.state == StatePlaying {
				return true
			}
		case <-ctx.Done():
			return false
		}
	}
	return !h.IsClosed() && h.state == StatePlaying
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

// MD5(IP:Port) 作为 Hub key
func (m *MultiChannelHub) HubKey(addr string) string {
	h := md5.Sum([]byte(addr))
	return hex.EncodeToString(h[:])
}

func (m *MultiChannelHub) GetOrCreateHub(udpAddr string, ifaces []string) (*StreamHub, error) {
	key := m.HubKey(udpAddr)
	logger.LogPrintf("🔑 GetOrCreateHub HubKey: %s", key)

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

	m.Mu.Lock()
	m.Hubs[key] = newHub
	m.Mu.Unlock()
	return newHub, nil
}

func (m *MultiChannelHub) RemoveHub(udpAddr string) {
	key := m.HubKey(udpAddr)
	m.Mu.Lock()
	defer m.Mu.Unlock()
	if hub, ok := m.Hubs[key]; ok {
		hub.Close()
		delete(m.Hubs, key)
		logger.LogPrintf("🗑️ Hub 已删除: %s", key)
	}
}

func (m *MultiChannelHub) CheckIsolation() {
	m.Mu.RLock()
	defer m.Mu.RUnlock()
	// 串台检查可根据需要扩展
}

// ====================
// 更新 UDPConn 网络接口
// ====================
func (h *StreamHub) UpdateInterfaces(ifaces []string) error {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	// if len(ifaces) == 0 {
	// 	return errors.New("至少指定一个网卡")
	// }

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
			conn, err := listenMulticast(udpAddr, []*net.Interface{iface})
			if err == nil {
				newConns = append(newConns, conn)
				break
			}
			lastErr = err
		}
		if conn == nil {
			// 最后尝试默认接口
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

	// 关闭旧连接
	for _, conn := range h.UdpConns {
		_ = conn.Close()
	}
	h.UdpConns = newConns

	// 启动新的 readLoop
	h.startReadLoops()
	logger.LogPrintf("✅ Hub UDPConn 已更新, 网卡=%v", ifaces)
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

// ====================
// 工具函数
// ====================
// 改进的格式检测函数
// 全局计数器，用于限制日志打印数量
var (
	keyFrameLogCount    int32
	nonKeyFrameLogCount int32
	maxLogCount         int32 = 10
)

// 添加格式自动检测的辅助函数
func detectStreamFormat(pkt []byte) string {
	// 检查TS格式: 第一个字节是否为0x47且包长为188的倍数
	if len(pkt) >= 1 && pkt[0] == TS_SYNC_BYTE && len(pkt)%TS_PACKET_SIZE == 0 {
		return "ts"
	}

	// 检查RTP格式: 版本字段为2
	if len(pkt) >= 1 {
		version := (pkt[0] >> 6) & 0x03
		if version == 2 {
			return "rtp"
		}
	}

	return "ts" // 默认TS格式
}

func (h *StreamHub) isKeyFrameByFormat(pkt []byte, format string) bool {
	var result bool
	var frameType string
	// 每次检测前重置全局状态
	h.hasSPS = false
	h.hasPPS = false
	switch format {
	case "ts":
		result = h.isKeyFrameTS(pkt)
	case "rtp":
		result = h.isKeyFrameRTP(pkt)
	default:
		// 自动检测格式
		if h.isKeyFrameTS(pkt) {
			result = true
		} else {
			result = h.isKeyFrameRTP(pkt)
		}
	}

	// 确定帧类型
	if result {
		frameType = "关键帧"
	} else {
		frameType = "非关键帧"
	}

	// 限制日志打印数量
	if result {
		if count := atomic.LoadInt32(&keyFrameLogCount); count < maxLogCount {
			if atomic.CompareAndSwapInt32(&keyFrameLogCount, count, count+1) {
				h.logFrameDetection(pkt, format, frameType, count+1)
			}
		}
	} else {
		if count := atomic.LoadInt32(&nonKeyFrameLogCount); count < maxLogCount {
			if atomic.CompareAndSwapInt32(&nonKeyFrameLogCount, count, count+1) {
				h.logFrameDetection(pkt, format, frameType, count+1)
			}
		}
	}

	return result
}

// 日志打印辅助函数
func (h *StreamHub) logFrameDetection(pkt []byte, format, frameType string, count int32) {
	pktLen := len(pkt)
	var preview string

	// 生成数据预览（前16字节）
	if pktLen > 0 {
		previewBytes := make([]string, 0)
		maxPreview := 16
		if pktLen < maxPreview {
			maxPreview = pktLen
		}
		for i := 0; i < maxPreview; i++ {
			previewBytes = append(previewBytes, fmt.Sprintf("%02X", pkt[i]))
		}
		preview = strings.Join(previewBytes, " ")
	}

	// 提取更多调试信息
	debugInfo := h.getFrameDebugInfo(pkt, format)

	logger.LogPrintf("🎯 帧检测 [%d/%d] 格式=%s 类型=%s 长度=%d 预览=%s %s",
		count, maxLogCount, format, frameType, pktLen, preview, debugInfo)
}

// 获取帧调试信息
func (h *StreamHub) getFrameDebugInfo(pkt []byte, format string) string {
	switch format {
	case "ts":
		return h.getTSDebugInfo(pkt)
	case "rtp":
		return h.getRTPDebugInfo(pkt)
	default:
		return h.getAutoDebugInfo(pkt)
	}
}

// TS格式调试信息
func (h *StreamHub) getTSDebugInfo(pkt []byte) string {
	if len(pkt) < 4 || pkt[0] != 0x47 {
		return "无效TS包"
	}

	pid := uint16(pkt[1]&0x1F)<<8 | uint16(pkt[2])
	adaptation := (pkt[3] >> 4) & 0x03
	hasPayload := adaptation == 0x01 || adaptation == 0x03

	return fmt.Sprintf("PID=0x%04X 适配字段=%d 有负载=%v", pid, adaptation, hasPayload)
}

// RTP格式调试信息
func (h *StreamHub) getRTPDebugInfo(pkt []byte) string {
	if len(pkt) < 12 {
		return "RTP包过短"
	}

	version := (pkt[0] >> 6) & 0x03
	padding := (pkt[0] >> 5) & 0x01
	extension := (pkt[0] >> 4) & 0x01
	csrcCount := pkt[0] & 0x0F

	marker := (pkt[1] >> 7) & 0x01
	payloadType := pkt[1] & 0x7F
	sequence := uint16(pkt[2])<<8 | uint16(pkt[3])
	timestamp := binary.BigEndian.Uint32(pkt[4:8])
	ssrc := binary.BigEndian.Uint32(pkt[8:12])

	return fmt.Sprintf("版本=%d 填充=%d 扩展=%d CSRC数量=%d 标记=%d 负载类型=%d 序列号=%d 时间戳=%d SSRC=%d",
		version, padding, extension, csrcCount, marker, payloadType, sequence, timestamp, ssrc)
}

// 自动检测格式的调试信息
func (h *StreamHub) getAutoDebugInfo(pkt []byte) string {
	if len(pkt) < 1 {
		return "空包"
	}

	// 尝试检测格式
	if pkt[0] == 0x47 && len(pkt)%188 == 0 {
		return "检测为TS格式"
	}

	version := (pkt[0] >> 6) & 0x03
	if version == 2 {
		return "检测为RTP格式"
	}

	return "格式未知"
}

// 重置日志计数器（可选，用于重新开始计数）
func ResetFrameLogCounters() {
	atomic.StoreInt32(&keyFrameLogCount, 0)
	atomic.StoreInt32(&nonKeyFrameLogCount, 0)
}

// 改进的TS关键帧检测
func (h *StreamHub) isKeyFrameTS(pkt []byte) bool {
	if len(pkt) != TS_PACKET_SIZE || pkt[0] != TS_SYNC_BYTE {
		return false
	}

	// Adaptation field + payload
	adaptation := (pkt[3] >> 4) & 0x03
	payloadStart := 4
	if adaptation == 2 || adaptation == 3 { // with adaptation field
		adaptLen := int(pkt[4])
		payloadStart += 1 + adaptLen
		if payloadStart >= TS_PACKET_SIZE {
			return false
		}
	}

	payload := pkt[payloadStart:]
	if len(payload) < 4 {
		return false
	}

	// 扫描 H.264 NALU
	for i := 0; i < len(payload)-4; i++ {
		if payload[i] == 0x00 && payload[i+1] == 0x00 {
			var naluType byte
			if payload[i+2] == 0x01 {
				naluType = payload[i+3] & 0x1F
			} else if payload[i+2] == 0x00 && payload[i+3] == 0x01 {
				naluType = payload[i+4] & 0x1F
			} else {
				continue
			}

			switch naluType {
			case 7: // SPS
				h.hasSPS = true
			case 8: // PPS
				h.hasPPS = true
			case 5: // IDR
				if h.hasSPS && h.hasPPS {
					return true
				}
			}
		}
	}
	return false
}

// 改进的RTP关键帧检测 - 专门处理TS over RTP
func (h *StreamHub) isKeyFrameRTP(pkt []byte) bool {
	if len(pkt) < 12 {
		return false
	}

	version := (pkt[0] >> 6) & 0x03
	if version != 2 {
		return false
	}

	// RTP 头
	csrcCount := int(pkt[0] & 0x0F)
	extension := (pkt[0] >> 4) & 0x01
	payloadType := pkt[1] & 0x7F
	headerLen := 12 + (4 * csrcCount)

	if extension == 1 {
		if len(pkt) < headerLen+4 {
			return false
		}
		extLen := int(binary.BigEndian.Uint16(pkt[headerLen+2:headerLen+4])) * 4
		headerLen += 4 + extLen
	}

	if len(pkt) <= headerLen {
		return false
	}
	payload := pkt[headerLen:]

	// ---------------- RTP MP2T 模式 (TS over RTP, PT=33) ----------------
	if payloadType == 33 {
		for i := 0; i+TS_PACKET_SIZE <= len(payload); i++ {
			if payload[i] == TS_SYNC_BYTE {
				if h.isKeyFrameTS(payload[i : i+TS_PACKET_SIZE]) {
					return true
				}
			}
		}
		return false
	}

	// ---------------- RTP H.264 模式 ----------------
	if len(payload) < 1 {
		return false
	}
	naluType := payload[0] & 0x1F

	switch naluType {
	case 1: // 非IDR帧
		return false
	case 5: // 完整IDR
		return true
	case 7: // SPS
		h.hasSPS = true
	case 8: // PPS
		h.hasPPS = true
	case 24: // STAP-A (多个NALU打包)
		offset := 1
		for offset+2 < len(payload) {
			nalSize := int(binary.BigEndian.Uint16(payload[offset : offset+2]))
			offset += 2
			if offset+nalSize > len(payload) {
				break
			}
			nalu := payload[offset]
			naluTypeInner := nalu & 0x1F
			if naluTypeInner == 7 {
				h.hasSPS = true
			} else if naluTypeInner == 8 {
				h.hasPPS = true
			} else if naluTypeInner == 5 && h.hasSPS && h.hasPPS {
				return true
			}
			offset += nalSize
		}
	case 28: // FU-A
		if len(payload) < 2 {
			return false
		}
		startBit := (payload[1] >> 7) & 0x01
		if startBit == 1 {
			fragNaluType := payload[1] & 0x1F
			if fragNaluType == 5 && h.hasSPS && h.hasPPS {
				return true
			}
		}
	}
	return false
}
