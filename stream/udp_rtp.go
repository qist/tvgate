package stream

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qist/tvgate/logger"
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
// StreamHub
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
	CacheBuffer    *RingBuffer
	DetectedFormat string // ts 或 rtp
	addr           string

	// 性能统计
	PacketCount uint64
	DropCount   uint64
	DelaySum    int64
	DelayCount  int64
}

var (
	Hubs   = make(map[string]*StreamHub)
	HubsMu sync.Mutex
)

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
	if len(ifaces) == 0 {
		conn, err = net.ListenMulticastUDP("udp", nil, addr)
		if err != nil {
			conn, err = net.ListenUDP("udp", addr)
			if err != nil {
				return nil, err
			}
		}
		logger.LogPrintf("🟢 监听 %s (默认接口)", udpAddr)
	} else {
		var lastErr error
		for _, name := range ifaces {
			iface, ierr := net.InterfaceByName(name)
			if ierr != nil {
				lastErr = ierr
				logger.LogPrintf("⚠️ 网卡 %s 不存在或不可用: %v", name, ierr)
				continue
			}
			conn, err = net.ListenMulticastUDP("udp", iface, addr)
			if err == nil {
				logger.LogPrintf("🟢 监听 %s@%s 成功", udpAddr, name)
				break
			}
			lastErr = err
			logger.LogPrintf("⚠️ 监听 %s@%s 失败: %v", udpAddr, name, err)
		}
		if conn == nil {
			conn, err = net.ListenUDP("udp", addr)
			if err != nil {
				return nil, fmt.Errorf("所有网卡监听失败且 UDP 监听失败: %v (last=%v)", err, lastErr)
			}
			logger.LogPrintf("🟡 回退为普通 UDP 监听 %s", udpAddr)
		}
	}

	_ = conn.SetReadBuffer(8 * 1024 * 1024)

	hub := &StreamHub{
		Clients:        make(map[chan []byte]struct{}),
		AddCh:          make(chan chan []byte, 1024),
		RemoveCh:       make(chan chan []byte, 1024),
		UdpConn:        conn,
		Closed:         make(chan struct{}),
		BufPool:        &sync.Pool{New: func() any { return make([]byte, 32*1024) }},
		CacheBuffer:    NewRingBuffer(4096), // 大约 10 秒缓存 (假设每帧 188 字节，每秒 1Mbps)
		addr:           udpAddr,
		DetectedFormat: "",
	}

	go hub.run()
	go hub.readLoop()

	logger.LogPrintf("UDP 监听地址：%s ifaces=%v", udpAddr, ifaces)
	return hub, nil
}

// ====================
// 客户端管理循环
// ====================
func (h *StreamHub) run() {
	for {
		select {
		case ch := <-h.AddCh:
			h.Mu.Lock()
			h.Clients[ch] = struct{}{}
			go h.sendInitial(ch)
			h.Mu.Unlock()
			logger.LogPrintf("➕ 客户端加入，当前=%d", len(h.Clients))

		case ch := <-h.RemoveCh:
			h.Mu.Lock()
			if _, ok := h.Clients[ch]; ok {
				delete(h.Clients, ch)
				close(ch)
			}
			clientCount := len(h.Clients)
			h.Mu.Unlock()
			logger.LogPrintf("➖ 客户端离开，当前=%d", clientCount)
			if clientCount == 0 {
				h.Close()
			}

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

// ====================
// 读取UDP并分发
// ====================
func (h *StreamHub) readLoop() {
	defer func() {
		if r := recover(); r != nil {
			logger.LogPrintf("readLoop recovered from panic: %v", r)
		}
	}()

	for {
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

		data := make([]byte, n)
		copy(data, buf[:n])
		h.BufPool.Put(buf)

		h.Mu.Lock()
		h.PacketCount++
		h.LastFrame = data
		h.CacheBuffer.Push(data)

		// 第一次接收数据自动检测格式
		// 改进的格式检测逻辑
		if h.DetectedFormat == "" {
			h.DetectedFormat = detectStreamFormat(data)
			// logger.LogPrintf("🔍 自动检测流类型: %s, 包长度: %d", h.DetectedFormat, len(data))
		}

		// 使用改进的关键帧检测
		keyFrame := h.isKeyFrameByFormat(data, h.DetectedFormat)
		if keyFrame {
			h.LastKeyFrame = data
			// logger.LogPrintf("🎯 检测到关键帧: 格式=%s, 长度=%d", h.DetectedFormat, len(data))
		}

		clients := make([]chan []byte, 0, len(h.Clients))
		for ch := range h.Clients {
			clients = append(clients, ch)
		}
		h.Mu.Unlock()

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
// 新客户端发送初始关键帧 + 后续帧
// ====================
func (h *StreamHub) sendInitial(ch chan []byte) {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	sentKey := false
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
	if !sentKey && h.LastKeyFrame != nil {
		select {
		case ch <- h.LastKeyFrame:
		default:
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

	for {
		select {
		case data, ok := <-ch:
			if !ok {
				return
			}
			n, err := w.Write(data)
			if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				logger.LogPrintf("写入客户端错误: %v", err)
				return
			}
			bufferedBytes += n

		case <-flushTicker.C:
			if flusher != nil && bufferedBytes > 0 {
				flusher.Flush()
				bufferedBytes = 0
			}

		case <-activeTicker.C:
			if updateActive != nil {
				updateActive()
			}

		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
			return
		case <-h.Closed:
			return
		}
	}
}

// ====================
// 客户端迁移
// ====================
func (h *StreamHub) TransferClientsTo(newHub *StreamHub) {
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
	defer h.Mu.Unlock()
	select {
	case <-h.Closed:
		return
	default:
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
	logger.LogPrintf("UDP监听已关闭，端口已释放: %s", h.addr)
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

	switch format {
	case "ts":
		result = isKeyFrameTS(pkt)
	case "rtp":
		result = isKeyFrameRTP(pkt)
	default:
		// 自动检测格式
		if isKeyFrameTS(pkt) {
			result = true
		} else {
			result = isKeyFrameRTP(pkt)
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
func isKeyFrameTS(pkt []byte) bool {
	// 首先检查是否为有效的TS包
	if !isValidTSPacket(pkt) {
		return false
	}

	// 提取负载
	payload := extractTSPayload(pkt)
	if payload == nil || len(payload) < 4 {
		return false
	}

	// 在负载中查找H.264起始码和关键帧
	for i := 0; i < len(payload)-4; i++ {
		// 查找起始码: 0x00 0x00 0x01 或 0x00 0x00 0x00 0x01
		if payload[i] == 0x00 && payload[i+1] == 0x00 {
			if payload[i+2] == 0x01 {
				// 3字节起始码
				if i+3 < len(payload) {
					naluType := payload[i+3] & 0x1F
					if naluType == 5 { // IDR帧
						return true
					}
				}
			} else if i+3 < len(payload) && payload[i+2] == 0x00 && payload[i+3] == 0x01 {
				// 4字节起始码
				if i+4 < len(payload) {
					naluType := payload[i+4] & 0x1F
					if naluType == 5 { // IDR帧
						return true
					}
				}
			}
		}
	}

	return false
}

// 改进的RTP关键帧检测
func isKeyFrameRTP(pkt []byte) bool {
	if len(pkt) < 12 {
		return false
	}

	// RTP头部验证
	version := (pkt[0] >> 6) & 0x03
	if version != 2 {
		return false
	}

	payload := pkt[12:]
	if len(payload) < 2 {
		return false
	}

	// 检查H.264的NAL单元类型
	// RTP H.264负载格式: 第一个字节的type字段
	naluType := payload[0] & 0x1F

	// 如果是分片单元，检查FU-A的起始分片和类型
	if naluType == 28 { // FU-A
		if len(payload) < 2 {
			return false
		}
		// FU indicator的type=28, FU header的S=1表示起始分片
		startBit := (payload[1] >> 7) & 0x01
		if startBit == 1 {
			fragmentedNaluType := payload[1] & 0x1F
			return fragmentedNaluType == 5 // IDR帧
		}
		return false
	}

	return naluType == 5 // 完整的IDR帧
}

func isMulticast(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	return ip4[0] >= 224 && ip4[0] <= 239
}

func HubKey(addr string, ifaces []string) string {
	return addr + "|" + strings.Join(ifaces, ",")
}

func GetOrCreateHub(udpAddr string, ifaces []string) (*StreamHub, error) {
	key := HubKey(udpAddr, ifaces)

	HubsMu.Lock()
	defer HubsMu.Unlock()

	if hub, ok := Hubs[key]; ok {
		select {
		case <-hub.Closed:
			delete(Hubs, key)
		default:
			return hub, nil
		}
	}

	newHub, err := NewStreamHub(udpAddr, ifaces)
	if err != nil {
		return nil, err
	}
	Hubs[key] = newHub
	return newHub, nil
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
