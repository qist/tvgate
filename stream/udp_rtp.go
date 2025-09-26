package stream

import (
	// "bytes"
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
	// "sync/atomic"
	// "sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ====================
// TS + RTP 高性能关键帧检测 + 缓存
// 支持 H.264/H.265 (HEVC)
// ====================

const TS_PACKET_SIZE = 188
const TS_SYNC_BYTE = 0x47

// H.264 NALU 类型
const (
	H264_IDR         = 5
	H264_SPS         = 7
	H264_PPS         = 8
	H264_STAP        = 24
	H264_FUA         = 28
	PS_START_CODE    = 0x000001BA
	MAX_BUFFER_SIZE  = 64 * 1024 // 累积8KB数据
	MAX_TS_SCAN_PKTS = 10        // TS 最多扫描前10个包
	MIN_DETECT_SIZE  = 188 * 5   // 至少5个TS包
)

// H.265 NALU 类型
const (
	HEVC_VPS   = 32
	HEVC_SPS   = 33
	HEVC_PPS   = 34
	HEVC_IDR_W = 19
	HEVC_IDR_N = 20
	HEVC_FU    = 49
)

// ====================
// 关键帧缓存结构
// ====================
type KeyFrameCache struct {
	mu     sync.RWMutex
	spspps map[byte][]byte
	frames [][]byte
	ts     []byte // 最近完整 TS 关键帧
	rtp    []byte // 最近完整 RTP 关键帧
	// sps     []byte
	// pps     []byte
	// vps     []byte // 如果是 H.265
	// lastTS  int64  // 可选：时间戳
	// lastRTP int64
}

// StreamFormat 封装类型 + 载荷类型
type StreamFormat struct {
	Container string // ts / rtp / ps / unknown
	Payload   string // h264 / h265 / unknown

}

// StreamDetector 用于累积多包数据检测
type StreamDetector struct {
	buffer   []byte
	detected *StreamFormat // 缓存首次检测结果
}

// NewStreamDetector 创建检测器
func NewStreamDetector() *StreamDetector {
	return &StreamDetector{
		buffer: make([]byte, 0, MAX_BUFFER_SIZE),
	}
}

// 清理缓存
// Reset 时清理 buffer 和已检测格式
func (d *StreamDetector) Reset() {
	d.buffer = d.buffer[:0]
	d.detected = nil
}

// Feed 输入新数据（UDP包）
func (d *StreamDetector) Feed(data []byte) StreamFormat {
	// 累积缓存
	d.buffer = append(d.buffer, data...)
	if len(d.buffer) > MAX_BUFFER_SIZE {
		d.buffer = d.buffer[len(d.buffer)-MAX_BUFFER_SIZE:]
	}

	// 缓存不足时返回 unknown
	if len(d.buffer) < MIN_DETECT_SIZE {
		return StreamFormat{Container: "unknown", Payload: "unknown"}
	}

	// 如果已检测过，直接返回缓存
	if d.detected != nil {
		return *d.detected
	}

	// 调用格式检测函数
	format := detectStreamFormat(d.buffer)

	// 缓存检测结果，后续客户端复用
	d.detected = &format

	// 日志输出（只输出首次检测）
	logFrameInfo(format, data, false)

	return format
}

func (d *StreamDetector) GetBuffer() []byte {
	return d.buffer
}
func NewKeyFrameCache() *KeyFrameCache {
	return &KeyFrameCache{
		spspps: make(map[byte][]byte),
		frames: make([][]byte, 0, 16),
	}
}
func (c *KeyFrameCache) AddSPSPPS(naluType byte, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.spspps[naluType] = append([]byte(nil), data...)
}

func (c *KeyFrameCache) AddFrame(data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.frames = append(c.frames, append([]byte(nil), data...))
	if len(c.frames) > 16 { // 限制缓存数量
		c.frames = c.frames[len(c.frames)-16:]
	}
}

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

// ----------------------
// 对象池定义
// ----------------------
var bufePool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 64*1024)
	},
}

var framePool = sync.Pool{
	New: func() interface{} {
		return make([][]byte, 0, 2048) // 每次最多缓存1024帧，可根据实际情况调整
	},
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
	Mu             sync.RWMutex
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
	DetectedFormat StreamFormat
	AddrList       []string
	PacketCount    uint64
	DropCount      uint64
	// hasSPS         bool
	// hasPPS         bool
	state         int // 0: stopped, 1: playing, 2: error
	stateCond     *sync.Cond
	OnEmpty       func(h *StreamHub) // 当客户端数量为0时触发
	KeyFrameCache *KeyFrameCache
	// 新增字段：多包累积检测器
	detector *StreamDetector
}

// ====================
// 创建新 Hub
// ====================
func NewStreamHub(addrs []string, ifaces []string) (*StreamHub, error) {
	if len(addrs) == 0 {
		return nil, fmt.Errorf("至少一个 UDP 地址")
	}

	hub := &StreamHub{
		Clients:       make(map[string]hubClient),
		AddCh:         make(chan hubClient, 1024),
		RemoveCh:      make(chan string, 1024),
		UdpConns:      make([]*net.UDPConn, 0, len(addrs)),
		CacheBuffer:   NewRingBuffer(8192), // 默认缓存8192帧
		Closed:        make(chan struct{}),
		BufPool:       &sync.Pool{New: func() any { return make([]byte, 64*1024) }},
		AddrList:      addrs,
		state:         StatePlaying,
		KeyFrameCache: NewKeyFrameCache(),
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
		// logger.LogPrintf("🟢 Listening on %s via interfaces %v", udpAddr, ifaces)
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
		logger.LogPrintf("❌ readLoop: conn is nil, hubAddr=%s", hubAddr)
		return
	}

	udpAddr, _ := net.ResolveUDPAddr("udp", hubAddr)
	dstIP := udpAddr.IP.String()

	pconn := ipv4.NewPacketConn(conn)
	_ = pconn.SetControlMessage(ipv4.FlagDst, true)

	for {
		select {
		case <-h.Closed:
			logger.LogPrintf("ℹ️ readLoop: hub closed, hubAddr=%s", hubAddr)
			return
		default:
		}

		buf := bufePool.Get().([]byte)
		n, cm, _, err := pconn.ReadFrom(buf)
		if err != nil {
			bufePool.Put(buf)
			if !errors.Is(err, net.ErrClosed) {
				logger.LogPrintf("❌ ReadFrom failed: %v, hubAddr=%s", err, hubAddr)
			}
			return
		}

		if cm != nil && cm.Dst.String() != dstIP {
			// bufePool.Put(buf)
			continue
		}

		data := make([]byte, n)
		copy(data, buf[:n])
		bufePool.Put(buf)

		h.Mu.RLock()
		closed := h.state == StateStopped || h.CacheBuffer == nil
		h.Mu.RUnlock()
		if closed {
			return
		}

		h.broadcast(data)
	}
}

// ====================
// 广播到所有客户端
// ====================
func (h *StreamHub) broadcast(data []byte) {
	var clients map[string]hubClient
	var lastKeyFrame bool

	h.Mu.Lock()
	if h.Closed == nil || h.CacheBuffer == nil || h.Clients == nil {
		h.Mu.Unlock()
		return
	}

	// 更新状态
	h.PacketCount++
	h.LastFrame = data
	h.CacheBuffer.Push(data)

	// 仅第一次客户端获取流格式
	if h.DetectedFormat.Container == "" {
		if h.detector == nil {
			h.detector = NewStreamDetector()
		}
		h.DetectedFormat = h.detector.Feed(data)
	}

	// 初始化关键帧缓存
	if h.KeyFrameCache == nil {
		h.KeyFrameCache = NewKeyFrameCache()
	}

	// 判断当前帧是否关键帧
	lastKeyFrame = h.isKeyFrameByFormat(data, h.DetectedFormat, h.KeyFrameCache)
	if lastKeyFrame {
		logFrameInfo(h.DetectedFormat, data, true)
		h.LastKeyFrame = data

		// 更新初始化缓存（slice 池复用）
		tmp := framePool.Get().([][]byte)
		tmp = tmp[:0]
		tmp = append(tmp, h.CacheBuffer.GetAll()...)
		if h.LastInitFrame != nil {
			framePool.Put(h.LastInitFrame)
		}
		h.LastInitFrame = tmp
	}

	// 播放状态更新
	if h.state != StatePlaying {
		h.state = StatePlaying
		h.stateCond.Broadcast()
	}

	// 拷贝客户端 map，解锁后发送
	clients = make(map[string]hubClient, len(h.Clients))
	for k, v := range h.Clients {
		clients[k] = v
	}
	h.Mu.Unlock()

	// 非阻塞广播数据
	for _, client := range clients {
		select {
		case client.ch <- data:
			// 发送成功
		default:
			// 丢帧处理
			h.Mu.Lock()
			h.DropCount++
			// 每丢100帧尝试恢复
			if h.DropCount%100 == 0 {
				// 清空一帧旧数据
				select {
				case <-client.ch:
				default:
				}
				// 发送关键帧恢复
				if h.LastKeyFrame != nil {
					select {
					case client.ch <- h.LastKeyFrame:
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
// sendInitial 发送初始化帧给新客户端，支持批量发送和智能丢帧
func (h *StreamHub) sendInitial(ch chan []byte) {
	// 获取缓存快照，锁粒度最小化
	h.Mu.Lock()
	cachedFrames := h.CacheBuffer.GetAll()
	detectedFormat := h.DetectedFormat
	keyCache := h.KeyFrameCache
	h.Mu.Unlock()

	go func() {
		// 从对象池取 slice
		frames := framePool.Get().([][]byte)
		defer framePool.Put(frames)

		// 清空并拷贝缓存
		frames = frames[:0]
		frames = append(frames, cachedFrames...)

		// 找到最近关键帧索引
		keyFrameIndex := -1
		for i := len(frames) - 1; i >= 0; i-- {
			if h.isKeyFrameByFormat(frames[i], detectedFormat, keyCache) {
				keyFrameIndex = i
				logFrameInfo(detectedFormat, frames[i], true)
				break
			}
		}

		start := 0
		if keyFrameIndex >= 0 {
			start = keyFrameIndex
		}

		// 从关键帧开始发送所有缓存帧
		for _, f := range frames[start:] {
			// 检查 hub 是否已关闭
			select {
			case <-h.Closed:
				return
			default:
			}

			// 非阻塞发送，防止 panic
			select {
			case ch <- f:
				// 数据透传，无限速
			default:
				// channel 已满或已关闭，直接退出
				return
			}
		}
	}()
}

// ====================
// HTTP 播放
// ====================
func (h *StreamHub) ServeHTTP(w http.ResponseWriter, r *http.Request, contentType string, updateActive func()) {
	// hubName := strings.Join(h.AddrList, ",")
	// logger.LogPrintf("DEBUG: Hub [%s] ServeHTTP 开始 - ClientIP: %s", hubName, r.RemoteAddr)

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

	// 增加缓冲区大小，避免ExoPlayer等播放器卡顿
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
	// 减少缓冲区大小，提高实时性，减少延迟
	const maxBufferSize = 128 * 1024 // 128KB缓冲区
	// 缩短刷新间隔，提高实时性
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
			// 降低缓冲阈值，更快刷新数据
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
		return
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

	// 关闭所有客户端
	for _, client := range h.Clients {
		if client.ch != nil {
			close(client.ch)
		}
	}
	h.Clients = nil

	// 清理该 HubKey 对应的检测器缓存
	if h.detector != nil {
		h.detector.Reset()
	}

	// 清理缓存
	h.CacheBuffer = nil
	h.LastFrame = nil
	h.LastKeyFrame = nil
	h.LastInitFrame = nil

	// 状态更新并广播
	h.state = StateStopped
	if h.stateCond != nil {
		h.stateCond.Broadcast()
	}

	if len(h.AddrList) > 0 {
		logger.LogPrintf("UDP监听已关闭，端口已释放: %s", h.AddrList[0])
	}
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
	// logger.LogPrintf("🔑 GetOrCreateHub HubKey: %s", key)

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
		GlobalMultiChannelHub.RemoveHub(h.AddrList[0])
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
		// logger.LogPrintf("🗑️ Hub 已删除: %s", key)
	}
}

// ====================
// 更新 Hub 的接口（只管 UDPConn 部分）
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
		for _, frame := range h.LastInitFrame {
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

// ====================
// 工具函数
// ====================

// 日志打印
func logFrameInfo(format StreamFormat, pkt []byte, preview bool) {
	if preview {
		logger.LogPrintf("🎯 格式={%s %s} 类型=%s 长度=%d 预览=% X", format.Container, format.Payload, format.Payload, len(pkt), pkt[:16])
	} else {
		logger.LogPrintf("🎯 格式={%s %s} 长度=%d", format.Container, format.Payload, len(pkt))
	}
}

// 关键帧检测统一接口
func (h *StreamHub) isKeyFrameByFormat(pkt []byte, format StreamFormat, cache *KeyFrameCache) bool {
	var isKey bool
	switch format.Container {
	case "ts":
		isKey = isKeyFrameTS(pkt, cache)
	case "rtp":
		isKey = isKeyFrameRTP(pkt, cache)
	default:
		switch format.Payload {
		case "h264":
			isKey = isKeyFrameH264(pkt, cache)
		case "h265":
			isKey = isKeyFrameH265(pkt, cache)
		default:
			isKey = false
		}
	}

	// 打印关键帧日志
	if isKey {
		logFrameInfo(format, pkt, true)
	}

	return isKey
}

// ====================
// 打印关键帧日志
// ====================
func logKeyFrame(format StreamFormat, pkt []byte) {
	logFrameInfo(format, pkt, true)
}

// ====================
// H.264 关键帧检测（TS/RTP 内部使用）
// ====================
func isKeyFrameH264(pkt []byte, cache *KeyFrameCache) bool {
	if len(pkt) < 4 {
		return false
	}

	hasSPS, hasPPS := false, false

	for i := 0; i < len(pkt)-4; i++ {
		if pkt[i] != 0x00 || pkt[i+1] != 0x00 {
			continue
		}

		var naluType byte
		var nalu []byte
		if pkt[i+2] == 0x01 {
			naluType = pkt[i+3] & 0x1F
			nalu = pkt[i+3:]
		} else if pkt[i+2] == 0x00 && pkt[i+3] == 0x01 {
			naluType = pkt[i+4] & 0x1F
			nalu = pkt[i+4:]
		} else {
			continue
		}

		switch naluType {
		case H264_SPS, H264_PPS:
			cache.AddSPSPPS(naluType, nalu)
			if naluType == H264_SPS {
				hasSPS = true
			} else if naluType == H264_PPS {
				hasPPS = true
			}
		case H264_IDR:
			if hasSPS && hasPPS {
				cache.AddFrame(pkt)
				logKeyFrame(StreamFormat{Container: "unknown", Payload: "h264"}, pkt)
				return true
			}
		}
	}

	return false
}

// ====================
// H.265 关键帧检测（TS/RTP 内部使用）
// ====================
func isKeyFrameH265(pkt []byte, cache *KeyFrameCache) bool {
	if len(pkt) < 5 {
		return false
	}

	hasSPS, hasPPS := false, false

	for i := 0; i < len(pkt)-5; i++ {
		if pkt[i] != 0x00 || pkt[i+1] != 0x00 {
			continue
		}

		var naluType byte
		var nalu []byte
		if pkt[i+2] == 0x01 {
			naluType = (pkt[i+3] >> 1) & 0x3F
			nalu = pkt[i+3:]
		} else if pkt[i+2] == 0x00 && pkt[i+3] == 0x01 {
			naluType = (pkt[i+4] >> 1) & 0x3F
			nalu = pkt[i+4:]
		} else {
			continue
		}

		switch naluType {
		case HEVC_VPS, HEVC_SPS, HEVC_PPS:
			cache.AddSPSPPS(naluType, nalu)
			if naluType == HEVC_SPS {
				hasSPS = true
			} else if naluType == HEVC_PPS {
				hasPPS = true
			}
		case HEVC_IDR_W, HEVC_IDR_N:
			if hasSPS && hasPPS {
				cache.AddFrame(pkt)
				logKeyFrame(StreamFormat{Container: "unknown", Payload: "h265"}, pkt)
				return true
			}
		}
	}

	return false
}

// ====================
// 3️⃣ TS 封装关键帧检测
// ====================
func isKeyFrameTS(pkt []byte, cache *KeyFrameCache) bool {
	// 复用你之前的 TS 关键帧检测逻辑
	if len(pkt) != TS_PACKET_SIZE || pkt[0] != TS_SYNC_BYTE {
		return false
	}

	adaptation := (pkt[3] >> 4) & 0x03
	payloadStart := 4
	if adaptation == 2 || adaptation == 3 {
		adaptLen := int(pkt[4])
		payloadStart += 1 + adaptLen
		if payloadStart >= TS_PACKET_SIZE {
			return false
		}
	}

	payload := pkt[payloadStart:]
	if len(payload) < 1 {
		return false
	}

	hasSPS, hasPPS := false, false

	for i := 0; i < len(payload)-4; i++ {
		if payload[i] != 0x00 || payload[i+1] != 0x00 {
			continue
		}

		var naluType byte
		var nalu []byte

		if payload[i+2] == 0x01 {
			naluType = payload[i+3] & 0x1F
			nalu = payload[i+3:]
		} else if payload[i+2] == 0x00 && payload[i+3] == 0x01 {
			naluType = payload[i+4] & 0x1F
			nalu = payload[i+4:]
		} else {
			continue
		}

		switch naluType {
		case H264_SPS, H264_PPS, HEVC_SPS, HEVC_PPS, HEVC_VPS:
			cache.AddSPSPPS(naluType, nalu)
			if naluType == H264_SPS || naluType == HEVC_SPS {
				hasSPS = true
			}
			if naluType == H264_PPS || naluType == HEVC_PPS {
				hasPPS = true
			}
		case H264_IDR, HEVC_IDR_W, HEVC_IDR_N:
			if hasSPS && hasPPS {
				cache.AddFrame(pkt)
				return true
			}
		}
	}

	return false
}

// ====================
// 4️⃣ RTP 封装关键帧检测
// ====================
func isKeyFrameRTP(pkt []byte, cache *KeyFrameCache) bool {
	if len(pkt) < 12 {
		return false
	}

	version := (pkt[0] >> 6) & 0x03
	if version != 2 {
		return false
	}

	csrcCount := int(pkt[0] & 0x0F)
	extension := (pkt[0] >> 4) & 0x01
	headerLen := 12 + 4*csrcCount

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
	if len(payload) < 1 {
		return false
	}

	payloadType := pkt[1] & 0x7F

	// TS over RTP
	if payloadType == 33 && len(payload) >= TS_PACKET_SIZE {
		for i := 0; i+TS_PACKET_SIZE <= len(payload); i += TS_PACKET_SIZE {
			if payload[i] == TS_SYNC_BYTE {
				if isKeyFrameTS(payload[i:i+TS_PACKET_SIZE], cache) {
					return true
				}
			}
		}
		return false
	}

	// H.264 / H.265 裸NALU
	naluType := payload[0] & 0x1F

	switch naluType {
	case H264_IDR:
		cache.AddFrame(pkt)
		return true
	case H264_STAP:
		offset := 1
		for offset+2 < len(payload) {
			nalSize := int(binary.BigEndian.Uint16(payload[offset : offset+2]))
			offset += 2
			if offset+nalSize > len(payload) {
				break
			}
			nt := payload[offset] & 0x1F
			if nt == H264_IDR {
				cache.AddFrame(pkt)
				return true
			}
			offset += nalSize
		}
	case H264_FUA:
		if len(payload) >= 2 && (payload[1]>>7)&0x01 == 1 && (payload[1]&0x1F) == H264_IDR {
			cache.AddFrame(pkt)
			return true
		}
	default:
		// H.265
		nt := (payload[0] >> 1) & 0x3F
		switch nt {
		case HEVC_IDR_W, HEVC_IDR_N:
			cache.AddFrame(pkt)
			return true
		case HEVC_FU:
			if len(payload) >= 3 && (payload[2]>>7)&0x01 == 1 {
				if (payload[2]&0x3F) == HEVC_IDR_W || (payload[2]&0x3F) == HEVC_IDR_N {
					cache.AddFrame(pkt)
					return true
				}
			}
		}
	}

	return false
}

// detectStreamFormat 核心检测逻辑
func detectStreamFormat(buf []byte) StreamFormat {
	if len(buf) == 0 {
		return StreamFormat{"unknown", "unknown"}
	}

	// --- TS检测 ---
	if isMPEGTSMulti(buf) {
		codec := detectVideoPayload(buf)
		return StreamFormat{"ts", codec}
	}

	// --- PS检测 ---
	if isMPEGPS(buf) {
		codec := detectVideoPayload(buf)
		return StreamFormat{"ps", codec}
	}

	// --- RTP/Raw H264/H265 ---
	codec := detectVideoPayload(buf)
	if codec != "unknown" {
		return StreamFormat{"rtp", codec} // rtp 这里表示裸H264/H265流
	}
	return StreamFormat{"unknown", "unknown"}
}

// isMPEGTSMulti 扫描多个TS包
func isMPEGTSMulti(buf []byte) bool {
	length := len(buf)
	maxScan := MAX_TS_SCAN_PKTS
	for offset := 0; offset < TS_PACKET_SIZE && offset < length; offset++ {
		count := 0
		for i := offset; i+TS_PACKET_SIZE <= length && count < maxScan; i += TS_PACKET_SIZE {
			if buf[i] != TS_SYNC_BYTE {
				break
			}
			count++
		}
		if count >= 3 { // 前3个包匹配
			return true
		}
	}
	return false
}

// isMPEGPS 检测PS起始码 0x000001BA
func isMPEGPS(buf []byte) bool {
	for i := 0; i+4 <= len(buf); i++ {
		if buf[i] == 0x00 && buf[i+1] == 0x00 && buf[i+2] == 0x01 && buf[i+3] == 0xBA {
			return true
		}
	}
	return false
}

// detectVideoPayload 扫描NALU头
func detectVideoPayload(buf []byte) string {
	if containsH264(buf) {
		return "h264"
	}
	if containsH265(buf) {
		return "h265"
	}
	return "unknown"
}

// --- 简单NALU检测 ---
func containsH264(buf []byte) bool {
	for i := 0; i+4 < len(buf); i++ {
		if buf[i] == 0x00 && buf[i+1] == 0x00 {
			if buf[i+2] == 0x00 && buf[i+3] == 0x01 {
				nal := buf[i+4] & 0x1F
				if nal >= 1 && nal <= 5 {
					return true
				}
			} else if buf[i+2] == 0x01 {
				nal := buf[i+3] & 0x1F
				if nal >= 1 && nal <= 5 {
					return true
				}
			}
		}
	}
	return false
}

func containsH265(buf []byte) bool {
	for i := 0; i+5 < len(buf); i++ {
		if buf[i] == 0x00 && buf[i+1] == 0x00 {
			if buf[i+2] == 0x00 && buf[i+3] == 0x01 {
				nalType := (buf[i+4] >> 1) & 0x3F
				if nalType >= 0 && nalType <= 31 {
					return true
				}
			} else if buf[i+2] == 0x01 {
				nalType := (buf[i+3] >> 1) & 0x3F
				if nalType >= 0 && nalType <= 31 {
					return true
				}
			}
		}
	}
	return false
}
