package stream

import (
	"context"
	"errors"
	"fmt"
	"github.com/qist/tvgate/logger"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// StreamHub 管理 UDP/组播流的多客户端转发
type StreamHub struct {
	Mu        sync.Mutex
	Clients   map[chan []byte]struct{}
	AddCh     chan chan []byte
	RemoveCh  chan chan []byte
	UdpConn   *net.UDPConn
	Closed    chan struct{}
	BufPool   *sync.Pool
	LastFrame []byte // 最近一帧，供秒开和热切换
}

var (
	Hubs   = make(map[string]*StreamHub)
	HubsMu sync.Mutex
)

func NewStreamHub(udpAddr string, ifaces []string) (*StreamHub, error) {
	addr, err := net.ResolveUDPAddr("udp", udpAddr)
	if err != nil {
		return nil, err
	}

	var conn *net.UDPConn
	if len(ifaces) == 0 {
		// 未指定网卡，优先多播，再降级普通 UDP
		conn, err = net.ListenMulticastUDP("udp", nil, addr)
		if err != nil {
			conn, err = net.ListenUDP("udp", addr)
			if err != nil {
				return nil, err
			}
		}
		logger.LogPrintf("🟢 监听 %s (默认接口)", udpAddr)
	} else {
		// 尝试每一个指定网卡，取第一个成功的
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
			// 所有网卡失败，尝试普通 UDP
			conn, err = net.ListenUDP("udp", addr)
			if err != nil {
				return nil, fmt.Errorf("所有网卡监听失败且 UDP 监听失败: %v (last=%v)", err, lastErr)
			}
			logger.LogPrintf("🟡 回退为普通 UDP 监听 %s", udpAddr)
		}
	}

	// 放大内核缓冲，尽可能减小丢包
	_ = conn.SetReadBuffer(4 * 1024 * 1024)

	hub := &StreamHub{
		Clients:  make(map[chan []byte]struct{}),
		AddCh:    make(chan chan []byte),
		RemoveCh: make(chan chan []byte),
		UdpConn:  conn,
		Closed:   make(chan struct{}),
		BufPool:  &sync.Pool{New: func() any { return make([]byte, 2048) }},
	}

	go hub.run()
	go hub.readLoop()

	logger.LogPrintf("UDP 监听地址：%s ifaces=%v", udpAddr, ifaces)
	return hub, nil
}

func (h *StreamHub) run() {
	for {
		select {
		case ch := <-h.AddCh:
			h.Mu.Lock()
			h.Clients[ch] = struct{}{}
			// 新客户端秒开：发一帧缓存
			if h.LastFrame != nil {
				select {
				case ch <- h.LastFrame:
				default:
				}
			}
			h.Mu.Unlock()
			logger.LogPrintf("➕ 客户端加入，当前=%d", len(h.Clients))

		case ch := <-h.RemoveCh:
			h.Mu.Lock()
			if _, ok := h.Clients[ch]; ok {
				delete(h.Clients, ch)
				close(ch)
			}
			h.Mu.Unlock()
			logger.LogPrintf("➖ 客户端离开，当前=%d", len(h.Clients))

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

func (h *StreamHub) readLoop() {
	for {
		buf := h.BufPool.Get().([]byte)
		n, _, err := h.UdpConn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-h.Closed:
				return
			default:
			}
			if !errors.Is(err, net.ErrClosed) {
				logger.LogPrintf("UDP 读取错误: %v", err)
			}
			time.Sleep(time.Second)
			continue
		}
		data := append([]byte(nil), buf[:n]...)
		h.BufPool.Put(buf)

		h.Mu.Lock()
		h.LastFrame = data
		h.Mu.Unlock()

		h.broadcast(data)
	}
}

func (h *StreamHub) broadcast(data []byte) {
	h.Mu.Lock()
	defer h.Mu.Unlock()
	for ch := range h.Clients {
		select {
		case ch <- data:
		default:
			// 丢包避免阻塞
		}
	}
}

func (h *StreamHub) ServeHTTP(w http.ResponseWriter, r *http.Request, contentType string) {
	ch := make(chan []byte, 20)
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
			writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			errCh := make(chan error, 1)
			go func() {
				_, err := w.Write(data)
				errCh <- err
			}()

			select {
			case err := <-errCh:
				cancel()
				if err != nil {
					if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
						logger.LogPrintf("写入客户端错误: %v", err)
					}
					return
				}
				flusher.Flush()
			case <-writeCtx.Done():
				cancel()
				logger.LogPrintf("写入超时，关闭连接")
				return
			}
		case <-ctx.Done():
			logger.LogPrintf("客户端断开连接")
			return
		case <-time.After(60 * time.Second):
			logger.LogPrintf("客户端空闲超时，关闭连接")
			return
		}
	}
}

func (h *StreamHub) TransferClientsTo(newHub *StreamHub) {
	h.Mu.Lock()
	defer h.Mu.Unlock()
	for ch := range h.Clients {
		// 先发新 Hub 的缓存帧，实现无缝切换
		newHub.Mu.Lock()
		if newHub.LastFrame != nil {
			select {
			case ch <- newHub.LastFrame:
			default:
			}
		}
		newHub.Mu.Unlock()
		newHub.AddCh <- ch
		delete(h.Clients, ch)
	}
}

func (h *StreamHub) Close() {
	h.Mu.Lock()
	select {
	case <-h.Closed:
		h.Mu.Unlock()
		return
	default:
		close(h.Closed)
	}
	if h.UdpConn != nil {
		_ = h.UdpConn.Close()
	}
	for ch := range h.Clients {
		close(ch)
	}
	h.Clients = nil
	h.Mu.Unlock()
}

func HubKey(addr string, ifaces []string) string {
	return addr + "|" + strings.Join(ifaces, ",")
}

func GetOrCreateHub(udpAddr string, ifaces []string) (*StreamHub, error) {
	key := HubKey(udpAddr, ifaces)
	HubsMu.Lock()
	if hub, ok := Hubs[key]; ok {
		HubsMu.Unlock()
		return hub, nil
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