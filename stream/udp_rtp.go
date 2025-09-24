package stream

import (
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/qist/tvgate/logger"
	"golang.org/x/net/ipv4"
	"io"
	"net"
	"net/http"
	// "runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	// "syscall"
	"time"
)

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

// TS包同步字节
const TS_SYNC_BYTE = 0x47
const TS_PACKET_SIZE = 188

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
// StreamHub 单频道 Hub
// ====================
type StreamHub struct {
	Mu             sync.Mutex
	Clients        map[chan []byte]struct{}
	AddCh          chan chan []byte
	RemoveCh       chan chan []byte
	UdpConn        *net.UDPConn
	Closed         chan struct{}
	BufPool        *sync.Pool
	LastFrame      []byte
	LastKeyFrame   []byte
	LastInitFrame  [][]byte // 保存 SPS/PPS + IDR
	CacheBuffer    *RingBuffer
	DetectedFormat string
	addr           string

	PacketCount uint64
	DropCount   uint64
	hasSPS      bool
	hasPPS      bool
}

var GlobalMultiChannelHub = NewMultiChannelHub()

// ====================
// 创建新Hub
// ====================
func NewStreamHub(udpAddr string, ifaces []string) (*StreamHub, error) {
	addr, err := net.ResolveUDPAddr("udp", udpAddr)
	if err != nil {
		return nil, err
	}
	if addr == nil || addr.IP == nil || !isMulticast(addr.IP) {
		return nil, fmt.Errorf("仅支持多播地址: %s", udpAddr)
	}

	var conn *net.UDPConn
	var lastErr error

	if len(ifaces) == 0 {
		// 无指定网卡时，尝试所有接口
		conn, err = listenMulticast(addr, nil)
		if err != nil {
			return nil, err
		}
	} else {
		// 遍历指定接口
		for _, name := range ifaces {
			iface, ierr := net.InterfaceByName(name)
			if ierr != nil {
				lastErr = ierr
				continue
			}
			conn, err = listenMulticast(addr, iface)
			if err == nil {
				break
			}
			lastErr = err
		}
		if conn == nil {
			return nil, fmt.Errorf("所有网卡监听失败: %v", lastErr)
		}
	}

	_ = conn.SetReadBuffer(8 * 1024 * 1024)

	hub := &StreamHub{
		Clients:     make(map[chan []byte]struct{}),
		AddCh:       make(chan chan []byte, 1024),
		RemoveCh:    make(chan chan []byte, 1024),
		UdpConn:     conn,
		Closed:      make(chan struct{}),
		BufPool:     &sync.Pool{New: func() any { return make([]byte, 32*1024) }},
		CacheBuffer: NewRingBuffer(4096),
		addr:        udpAddr,
	}

	go hub.run()
	go hub.readLoop()

	return hub, nil
}

// 封装跨平台多播监听
func listenMulticast(addr *net.UDPAddr, iface *net.Interface) (*net.UDPConn, error) {
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("ListenUDP failed: %w", err)
	}

	// 设置 socket 选项 (跨平台)
	if raw, err := conn.SyscallConn(); err == nil {
		raw.Control(func(fd uintptr) {
			setReuse(fd)
		})
	}

	// 加入多播组
	p := ipv4.NewPacketConn(conn)
	if iface != nil {
		if err := p.JoinGroup(iface, addr); err != nil {
			conn.Close()
			return nil, fmt.Errorf("JoinGroup failed: %w", err)
		}
	}

	return conn, nil
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
			h.checkIsolation()
		case ch := <-h.AddCh:
			h.Mu.Lock()
			h.Clients[ch] = struct{}{}
			go h.sendInitial(ch)
			h.Mu.Unlock()
		case ch := <-h.RemoveCh:
			h.Mu.Lock()
			if _, ok := h.Clients[ch]; ok {
				delete(h.Clients, ch)
				close(ch)
			}
			if len(h.Clients) == 0 {
				h.Mu.Unlock()
				h.Close()
				return
			}
			h.Mu.Unlock()
		case <-h.Closed:
			h.Mu.Lock()
			for ch := range h.Clients {
				close(ch)
			}
			h.Clients = nil
			h.Mu.Unlock()
			return
		}
	}
}

func (h *StreamHub) checkIsolation() {
	h.Mu.Lock()
	defer h.Mu.Unlock()
	for _, hub := range GlobalMultiChannelHub.Hubs {
		if hub != h && hub.UdpConn == h.UdpConn {
			logger.LogPrintf("CRITICAL: 检测到连接共享! %s 与 %s", h.addr, hub.addr)
		}
	}
}

// ====================
// 读取UDP并分发
// ====================
func (h *StreamHub) readLoop() {
	defer func() {
		if r := recover(); r != nil {
			logger.LogPrintf("❌ readLoop recovered from panic: %v", r)
		}
	}()

	for {
		// 检查是否已经关闭
		select {
		case <-h.Closed:
			return
		default:
		}

		if h.UdpConn == nil {
			logger.LogPrintf("⚠️ readLoop exit: UdpConn is nil")
			return
		}

		buf := h.BufPool.Get().([]byte)
		n, _, err := h.UdpConn.ReadFromUDP(buf)
		if err != nil {
			h.BufPool.Put(buf)
			select {
			case <-h.Closed:
				return
			default:
				continue
			}
		}

		// 拷贝数据
		data := make([]byte, n)
		copy(data, buf[:n])
		h.BufPool.Put(buf)

		// 写入Hub状态
		h.Mu.Lock()
		if h.CacheBuffer == nil {
			h.Mu.Unlock()
			return
		}

		h.PacketCount++
		h.LastFrame = data
		h.CacheBuffer.Push(data)

		// 自动探测格式
		if h.DetectedFormat == "" {
			h.DetectedFormat = detectStreamFormat(data)
		}

		// 检测关键帧
		if h.DetectedFormat != "" && h.isKeyFrameByFormat(data, h.DetectedFormat) {
			// 保存 LastKeyFrame
			h.LastKeyFrame = data

			// 保存完整初始化帧: 最近若干缓存 + 当前关键帧
			cached := h.CacheBuffer.GetAll()
			h.LastInitFrame = append(h.LastInitFrame[:0], cached...) // 保留缓存里关键帧前的SPS/PPS
		}

		// 拷贝客户端列表
		clients := make([]chan []byte, 0, len(h.Clients))
		for ch := range h.Clients {
			clients = append(clients, ch)
		}
		h.Mu.Unlock()

		// 投递数据到客户端
		for _, ch := range clients {
			select {
			case ch <- data:
			default:
				h.Mu.Lock()
				h.DropCount++
				h.Mu.Unlock()
			}
		}
	}
}
// ====================
// 新客户端发送完整初始化帧 + 后续帧
// ====================
func (h *StreamHub) sendInitial(ch chan []byte) {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	// 发送完整初始化帧 (SPS/PPS + IDR)
	if len(h.LastInitFrame) > 0 {
		for _, f := range h.LastInitFrame {
			select {
			case ch <- f:
			default:
			}
		}
		return
	}

	// 缓存中查找最近的关键帧
	var sentKey bool
	for _, f := range h.CacheBuffer.GetAll() {
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
// HTTP 推流
// ====================
func (h *StreamHub) ServeHTTP(w http.ResponseWriter, r *http.Request, contentType string, updateActive func()) {
	select {
	case <-h.Closed:
		http.Error(w, "Stream hub closed", http.StatusServiceUnavailable)
		return
	default:
	}

	ch := make(chan []byte, 1024)
	h.AddCh <- ch
	defer func() { h.RemoveCh <- ch }()

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

	clientClosed := false
	for !clientClosed {
		select {
		case data, ok := <-ch:
			if !ok {
				return
			}
			n, err := w.Write(data)
			if err != nil {
				if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
					logger.LogPrintf("写入客户端错误: %v", err)
				}
				clientClosed = true
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
			clientClosed = true
			return
		case <-h.Closed:
			clientClosed = true
			return
		}
	}
}

// ====================
// 客户端迁移
// ====================
func (h *StreamHub) TransferClientsTo(newHub *StreamHub) {
	if h.addr != newHub.addr {
		logger.LogPrintf("❌ 禁止跨频道迁移客户端: %s -> %s", h.addr, newHub.addr)
		return
	}

	h.Mu.Lock()
	defer h.Mu.Unlock()

	newHub.Mu.Lock()
	if newHub.Clients == nil {
		newHub.Clients = make(map[chan []byte]struct{})
	}
	newHub.CacheBuffer = NewRingBuffer(h.CacheBuffer.size)
	for _, f := range h.CacheBuffer.GetAll() {
		newHub.CacheBuffer.Push(f)
	}
	for ch := range h.Clients {
		newHub.Clients[ch] = struct{}{}
		if len(h.LastFrame) > 0 {
			select {
			case ch <- h.LastFrame:
			default:
			}
		}
	}
	newHub.Mu.Unlock()

	h.Clients = make(map[chan []byte]struct{})
	logger.LogPrintf("🔄 客户端已迁移到新Hub，数量=%d", len(newHub.Clients))
}

// ====================
// 接口更新
// ====================
func (h *StreamHub) UpdateInterfaces(udpAddr string, ifaces []string) error {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	addr, err := net.ResolveUDPAddr("udp", udpAddr)
	if err != nil {
		return err
	}

	var newConn *net.UDPConn
	if len(ifaces) == 0 {
		newConn, err = net.ListenMulticastUDP("udp", nil, addr)
		if err != nil {
			newConn, err = net.ListenUDP("udp", addr)
			if err != nil {
				return err
			}
		}
	} else {
		var lastErr error
		for _, name := range ifaces {
			iface, ierr := net.InterfaceByName(name)
			if ierr != nil {
				lastErr = ierr
				continue
			}
			newConn, err = net.ListenMulticastUDP("udp", iface, addr)
			if err == nil {
				break
			}
			lastErr = err
		}
		if newConn == nil {
			newConn, err = net.ListenUDP("udp", addr)
			if err != nil {
				return fmt.Errorf("所有网卡监听失败且 UDP 监听失败: %v (last=%v)", err, lastErr)
			}
		}
	}

	_ = newConn.SetReadBuffer(8 * 1024 * 1024)

	if h.UdpConn != nil {
		_ = h.UdpConn.Close()
	}
	h.UdpConn = newConn
	h.addr = udpAddr

	logger.LogPrintf("UDP 监听地址更新：%s ifaces=%v", udpAddr, ifaces)
	return nil
}

// ====================
// 关闭Hub
// ====================

func (h *StreamHub) Close() {
	h.Mu.Lock()
	if h.Closed != nil {
		select {
		case <-h.Closed:
			h.Mu.Unlock()
			return
		default:
		}
		close(h.Closed)
	}

	if h.UdpConn != nil {
		_ = h.UdpConn.Close()
		h.UdpConn = nil
	}

	for ch := range h.Clients {
		close(ch)
	}
	h.Clients = nil
	h.CacheBuffer = nil
	h.Mu.Unlock()

	// 不要在这里移除 MultiChannelHub，由 MultiChannelHub 管理
	// GlobalMultiChannelHub.RemoveHub(h.addr, nil)
}


// ====================
// MultiChannelHub 管理所有 Hub
// ====================
type MultiChannelHub struct {
	Mu   sync.RWMutex
	Hubs map[string]*StreamHub
}

func NewMultiChannelHub() *MultiChannelHub {
	return &MultiChannelHub{
		Hubs: make(map[string]*StreamHub),
	}
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

// ==============================
// MultiChannelHub 核心逻辑
// ==============================

func (m *MultiChannelHub) HubKey(addr string, ifaces []string) string {
	// 解析 IP:Port
	uAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		// 出错回退为原始字符串
		uAddr = &net.UDPAddr{IP: net.ParseIP(addr), Port: 0}
	}
	ipPort := fmt.Sprintf("%s_%d", uAddr.IP.String(), uAddr.Port)

	// 接口去重并排序
	ifaceMap := make(map[string]struct{})
	for _, iface := range ifaces {
		iface = strings.TrimSpace(strings.ToLower(iface))
		if iface != "" {
			ifaceMap[iface] = struct{}{}
		}
	}

	uniqueIfaces := make([]string, 0, len(ifaceMap))
	for iface := range ifaceMap {
		uniqueIfaces = append(uniqueIfaces, iface)
	}
	sort.Strings(uniqueIfaces)

	// 最终键 = IP_PORT|iface1#iface2
	return ipPort + "|" + strings.Join(uniqueIfaces, "#")
}

// 获取或创建 Hub，确保同一 UDPAddr + 接口唯一
func (m *MultiChannelHub) GetOrCreateHub(udpAddr string, ifaces []string) (*StreamHub, error) {
	key := m.HubKey(udpAddr, ifaces)
	logger.LogPrintf("🔑 GetOrCreateHub HubKey: %s", key)

	m.Mu.RLock()
	hub, exists := m.Hubs[key]
	m.Mu.RUnlock()

	if exists {
		if hub.IsClosed() { // Hub 自身提供状态方法，不直接用通道判断
			logger.LogPrintf("⚠️ Hub 已关闭，安全移除: %s", key)
			m.RemoveHub(udpAddr, ifaces)
		} else {
			return hub, nil
		}
	}

	newHub, err := NewStreamHub(udpAddr, ifaces)
	if err != nil {
		return nil, err
	}

	m.Mu.Lock()
	m.Hubs[key] = newHub
	m.Mu.Unlock()

	m.CheckIsolation()
	return newHub, nil
}

// 判断 Hub 是否已关闭
func (h *StreamHub) IsClosed() bool {
	select {
	case <-h.Closed:
		return true
	default:
		return false
	}
}

// 删除指定 Hub
func (m *MultiChannelHub) RemoveHub(udpAddr string, ifaces []string) {
	key := m.HubKey(udpAddr, ifaces)
	m.Mu.Lock()
	defer m.Mu.Unlock()
	if hub, ok := m.Hubs[key]; ok {
		hub.Close()
		delete(m.Hubs, key)
		logger.LogPrintf("🗑️ Hub 已删除: %s", key)
	}
}

// 检查是否存在串台（同一 UDPConn 被多个 Hub 使用）
func (m *MultiChannelHub) CheckIsolation() {
	m.Mu.RLock()
	defer m.Mu.RUnlock()
	for key1, hub1 := range m.Hubs {
		for key2, hub2 := range m.Hubs {
			if key1 == key2 {
				continue
			}
			if hub1.UdpConn != nil && hub1.UdpConn == hub2.UdpConn {
				logger.LogPrintf("⚠️ 串台检测: Hub %s 与 Hub %s 共用同一 UDPConn", hub1.addr, hub2.addr)
			}
		}
	}
}


// ====================
// 工具函数
// ====================
func isMulticast(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	return ip4[0] >= 224 && ip4[0] <= 239
}

// 检查是否为有效的TS包
func isValidTSPacket(pkt []byte) bool {
	if len(pkt) < TS_PACKET_SIZE {
		return false
	}
	return pkt[0] == TS_SYNC_BYTE
}

// 从TS包中提取负载
func extractTSPayload(pkt []byte) []byte {
	if len(pkt) < 4 || pkt[0] != TS_SYNC_BYTE {
		return nil
	}

	// 检查适配字段控制
	adaptFieldCtrl := (pkt[3] >> 4) & 0x03
	payloadStart := 4 // 基本包头长度

	// 处理适配字段
	if adaptFieldCtrl == 0x02 || adaptFieldCtrl == 0x03 {
		adaptFieldLen := int(pkt[4])
		if len(pkt) < 5+adaptFieldLen {
			return nil
		}
		payloadStart = 5 + adaptFieldLen
	}

	if payloadStart >= len(pkt) {
		return nil
	}

	return pkt[payloadStart:]
}
