package stream

import (
	"context"
	"errors"
	"fmt"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/monitor"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// 高性能 StreamHub
type StreamHub struct {
	clientsMu sync.RWMutex
	clients   map[*Client]struct{}
	addCh     chan *Client
	removeCh  chan *Client
	udpConn   *net.UDPConn
	closed    chan struct{}
	bufPool   *sync.Pool
	lastFrame atomic.Pointer[[]byte] // 最近一帧
}

// 单客户端
type Client struct {
	ch       chan []byte
	ctx      context.Context
	cancel   context.CancelFunc
	lastSent time.Time
}

var (
	Hubs   = make(map[string]*StreamHub)
	HubsMu sync.Mutex
)

// 创建 StreamHub
func NewStreamHub(udpAddr string, ifaces []string) (*StreamHub, error) {
	addr, err := net.ResolveUDPAddr("udp", udpAddr)
	if err != nil {
		return nil, err
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

	_ = conn.SetReadBuffer(8 * 1024 * 1024) // 大缓冲区

	hub := &StreamHub{
		clients:  make(map[*Client]struct{}),
		addCh:    make(chan *Client),
		removeCh: make(chan *Client),
		udpConn:  conn,
		closed:   make(chan struct{}),
		bufPool:  &sync.Pool{New: func() any { return make([]byte, 32*1024) }},
	}

	go hub.run()
	go hub.readLoop()

	logger.LogPrintf("UDP 监听地址：%s ifaces=%v", udpAddr, ifaces)
	return hub, nil
}

// Hub 主循环
func (h *StreamHub) run() {
	for {
		select {
		case client := <-h.addCh:
			h.clientsMu.Lock()
			h.clients[client] = struct{}{}
			if frame := h.lastFrame.Load(); frame != nil {
				select {
				case client.ch <- *frame:
				default:
				}
			}
			h.clientsMu.Unlock()
			go h.serveClient(client)

		case client := <-h.removeCh:
			h.clientsMu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.ch)
				client.cancel()
			}
			h.clientsMu.Unlock()
			if len(h.clients) == 0 {
				h.Close()
			}

		case <-h.closed:
			h.clientsMu.Lock()
			for client := range h.clients {
				close(client.ch)
				client.cancel()
			}
			h.clients = nil
			h.clientsMu.Unlock()
			return
		}
	}
}

// UDP 读取循环
func (h *StreamHub) readLoop() {
	for {
		select {
		case <-h.closed:
			return
		default:
		}

		buf := h.bufPool.Get().([]byte)
		n, _, err := h.udpConn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-h.closed:
				return
			default:
			}
			if !errors.Is(err, net.ErrClosed) {
				logger.LogPrintf("UDP 读取错误: %v", err)
			}
			time.Sleep(time.Millisecond * 100)
			continue
		}

		data := buf[:n]
		h.lastFrame.Store(&data)
		monitor.AddAppInboundBytes(uint64(n))
		h.broadcast(data)
	}
}

// 广播数据到所有客户端
func (h *StreamHub) broadcast(data []byte) {
	h.clientsMu.RLock()
	defer h.clientsMu.RUnlock()
	for client := range h.clients {
		select {
		case client.ch <- data:
		default:
			// 客户端队列满，丢帧
		}
	}
}

// 客户端处理
func (h *StreamHub) serveClient(client *Client) {
	flusher, ok := client.ctx.Value("writerFlusher").(http.Flusher)
	writer, ok2 := client.ctx.Value("writer").(http.ResponseWriter)
	if !ok || !ok2 {
		return
	}

	for {
		select {
		case <-client.ctx.Done():
			return
		case data, ok := <-client.ch:
			if !ok {
				return
			}
			_, err := writer.Write(data)
			if err != nil {
				if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
					logger.LogPrintf("写入客户端错误: %v", err)
				}
				return
			}
			flusher.Flush()
			monitor.AddAppOutboundBytes(uint64(len(data)))
			client.lastSent = time.Now()
		case <-time.After(60 * time.Second):
			return
		}
	}
}

// HTTP 流式接口
func (h *StreamHub) ServeHTTP(w http.ResponseWriter, r *http.Request, contentType string, updateActive func()) {
	select {
	case <-h.closed:
		http.Error(w, "Stream hub closed", http.StatusServiceUnavailable)
		return
	default:
	}

	w.Header().Set("Content-Type", contentType)
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	client := &Client{
		ch:       make(chan []byte, 50),
		ctx:      ctx,
		cancel:   cancel,
		lastSent: time.Now(),
	}

	ctx = context.WithValue(ctx, "writer", w)
	ctx = context.WithValue(ctx, "writerFlusher", flusher)
	client.ctx = ctx

	h.addCh <- client
	defer func() { h.removeCh <- client }()

	<-ctx.Done()
}

// TransferClientsTo 将客户端迁移到新 hub（秒开热切换）
func (h *StreamHub) TransferClientsTo(newHub *StreamHub) {
	h.clientsMu.Lock()
	defer h.clientsMu.Unlock()

	for client := range h.clients {
		if frame := newHub.lastFrame.Load(); frame != nil {
			select {
			case client.ch <- *frame:
			default:
			}
		}
		newHub.addCh <- client
		delete(h.clients, client)
	}
}

// 关闭 hub
func (h *StreamHub) Close() {
	select {
	case <-h.closed:
		return
	default:
		close(h.closed)
	}
	if h.udpConn != nil {
		_ = h.udpConn.Close()
	}

	HubsMu.Lock()
	for key, hub := range Hubs {
		if hub == h {
			delete(Hubs, key)
			break
		}
	}
	HubsMu.Unlock()

	logger.LogPrintf("UDP监听已关闭")
}

// Hub Key
func HubKey(addr string, ifaces []string) string {
	return addr + "|" + strings.Join(ifaces, ",")
}

// 获取或创建 hub
func GetOrCreateHub(udpAddr string, ifaces []string) (*StreamHub, error) {
	key := HubKey(udpAddr, ifaces)
	HubsMu.Lock()
	if hub, ok := Hubs[key]; ok {
		select {
		case <-hub.closed:
			delete(Hubs, key)
		default:
			HubsMu.Unlock()
			return hub, nil
		}
	}
	HubsMu.Unlock()

	hub, err := NewStreamHub(udpAddr, ifaces)
	if err != nil {
		return nil, err
	}

	HubsMu.Lock()
	Hubs[key] = hub
	HubsMu.Unlock()
	return hub, nil
}
