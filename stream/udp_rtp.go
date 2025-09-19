package stream

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
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
	Mu            sync.Mutex
	Clients       map[chan []byte]struct{}
	AddCh         chan chan []byte
	RemoveCh      chan chan []byte
	UdpConn       *net.UDPConn
	Closed        chan struct{}
	BufPool       *sync.Pool
	LastFrame     []byte
	LastKeyFrame  []byte
	CacheBuffer   *RingBuffer
	DetectedFormat string // ts 或 rtp
	addr          string

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
		Clients:      make(map[chan []byte]struct{}),
		AddCh:        make(chan chan []byte, 1024),
		RemoveCh:     make(chan chan []byte, 1024),
		UdpConn:      conn,
		Closed:       make(chan struct{}),
		BufPool:      &sync.Pool{New: func() any { return make([]byte, 32*1024) }},
		CacheBuffer:  NewRingBuffer(300),
		addr:         udpAddr,
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
		if h.DetectedFormat == "" {
			if isKeyFrameTS(data) {
				h.DetectedFormat = "ts"
			} else if isKeyFrameRTP(data) {
				h.DetectedFormat = "rtp"
			} else {
				h.DetectedFormat = "ts" // 默认 TS
			}
			logger.LogPrintf("🔍 自动检测流类型: %s", h.DetectedFormat)
		}

		if isKeyFrameByFormat(data, h.DetectedFormat) {
			h.LastKeyFrame = data
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
		if !sentKey && isKeyFrameByFormat(f, h.DetectedFormat) {
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
	for {
		select {
		case data, ok := <-ch:
			if !ok {
				return
			}
			_, err := w.Write(data)
			if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				logger.LogPrintf("写入客户端错误: %v", err)
				return
			}
			flusher.Flush()
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
func isKeyFrameByFormat(pkt []byte, format string) bool {
	switch format {
	case "ts":
		return isKeyFrameTS(pkt)
	case "rtp":
		return isKeyFrameRTP(pkt)
	default:
		return isKeyFrameTS(pkt)
	}
}

func isKeyFrameTS(pkt []byte) bool {
	if len(pkt) < 188 {
		return false
	}
	for i := 0; i < len(pkt)-4; i++ {
		if pkt[i] == 0x00 && pkt[i+1] == 0x00 && pkt[i+2] == 0x01 {
			naluType := pkt[i+3] & 0x1F
			if naluType == 5 {
				return true
			}
		}
	}
	return false
}

func isKeyFrameRTP(pkt []byte) bool {
	if len(pkt) < 12 {
		return false
	}
	payload := pkt[12:]
	if len(payload) < 1 {
		return false
	}
	naluType := payload[0] & 0x1F
	return naluType == 5
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
