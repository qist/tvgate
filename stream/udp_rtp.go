package stream

import (
	"context"
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

// StreamHub 管理 UDP/组播流的多客户端转发
type StreamHub struct {
	Mu          sync.Mutex
	Clients     map[chan []byte]struct{}
	AddCh       chan chan []byte
	RemoveCh    chan chan []byte
	UdpConn     *net.UDPConn
	Closed      chan struct{}
	BufPool     *sync.Pool
	LastFrame   []byte
	CacheBuffer [][]byte // 缓存最近的数据包，用于热切换
	Format      string   // 流格式（如HLS、RTMP等）
	addr        string   // 监听地址
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

	// 增大内核缓冲区，尽可能减小丢包
	_ = conn.SetReadBuffer(8 * 1024 * 1024)

	hub := &StreamHub{
		Clients:     make(map[chan []byte]struct{}),
		AddCh:       make(chan chan []byte, 100), // 增大通道缓冲
		RemoveCh:    make(chan chan []byte, 100), // 增大通道缓冲
		UdpConn:     conn,
		Closed:      make(chan struct{}),
		BufPool:     &sync.Pool{New: func() any { return make([]byte, 4096) }}, // 增大缓冲区
		CacheBuffer: make([][]byte, 0, 50),                                     // 初始化缓存缓冲区，用于热切换
		addr:        udpAddr,
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
			// 新客户端秒开：发送缓存的数据包以提高热切换流畅性
			for _, pkt := range h.CacheBuffer {
				select {
				case ch <- pkt:
				default:
					// 如果客户端通道已满，跳过以避免阻塞
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
			clientCount := len(h.Clients)
			h.Mu.Unlock()
			logger.LogPrintf("➖ 客户端离开，当前=%d", clientCount)

			// 如果没有客户端了，关闭UDP监听
			if clientCount == 0 {
				logger.LogPrintf("⏹ 没有客户端，立即关闭 Hub")
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

func (h *StreamHub) readLoop() {
	// 检查是否已经关闭
	select {
	case <-h.Closed:
		return
	default:
	}

	for {
		buf := h.BufPool.Get().([]byte)
		n, _, err := h.UdpConn.ReadFromUDP(buf)
		if err != nil {
			h.BufPool.Put(buf)
			select {
			case <-h.Closed:
				return
			default:
				// 检查是否还有客户端连接
				h.Mu.Lock()
				clientCount := len(h.Clients)
				h.Mu.Unlock()

				if clientCount == 0 {
					logger.LogPrintf("没有客户端，停止接收数据并关闭连接: %s", h.addr)
					h.Close()
					return
				}
				continue
			}
		}

		// 检查是否还有客户端连接
		h.Mu.Lock()
		clientCount := len(h.Clients)
		if clientCount == 0 {
			h.Mu.Unlock()
			h.BufPool.Put(buf[:cap(buf)])
			// 没有客户端，但继续监听以防新客户端加入
			continue
		}

		// 复制数据以避免竞态
		data := make([]byte, n)
		copy(data, buf[:n])
		h.BufPool.Put(buf[:cap(buf)])

		// 统计入流量
		// monitor.AddAppInboundBytes(uint64(len(data)))

		// 更新最近一帧
		h.LastFrame = data

		// 缓存数据包用于热切换
		if len(h.CacheBuffer) >= 50 {
			// 移除最旧的数据包
			copy(h.CacheBuffer, h.CacheBuffer[1:])
			h.CacheBuffer = h.CacheBuffer[:len(h.CacheBuffer)-1]
		}
		h.CacheBuffer = append(h.CacheBuffer, data)

		// 广播数据到所有客户端
		for ch := range h.Clients {
			select {
			case ch <- data:
			default:
				// 如果通道缓冲区满了，断开客户端
				close(ch)
				delete(h.Clients, ch)
			}
		}
		h.Mu.Unlock()
	}
}

func (h *StreamHub) ServeHTTP(w http.ResponseWriter, r *http.Request, contentType string, updateActive func()) {
	select {
	case <-h.Closed:
		http.Error(w, "Stream hub closed", http.StatusServiceUnavailable)
		return
	default:
	}

	// 增大客户端通道缓冲区以减少丢包
	ch := make(chan []byte, 200)
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
				n, err := w.Write(data)
				if err == nil {
					// monitor.AddAppOutboundBytes(uint64(n))
					_ = n // 避免未使用报错
				}
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
				if updateActive != nil {
					updateActive()
				}
			case <-writeCtx.Done():
				cancel()
				logger.LogPrintf("写入超时，关闭连接")
				return
			case <-h.Closed:
				cancel()
				logger.LogPrintf("Hub关闭，断开客户端连接")
				return
			}
		case <-ctx.Done():
			logger.LogPrintf("客户端断开连接")
			return
		case <-time.After(30 * time.Second): // 缩短超时时间以更快检测断开连接
			logger.LogPrintf("客户端空闲超时，关闭连接")
			return
		}
	}
}

func (h *StreamHub) TransferClientsTo(newHub *StreamHub) {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	// 检查newHub是否已初始化
	if newHub.Clients == nil {
		newHub.Mu.Lock()
		if newHub.Clients == nil {
			newHub.Clients = make(map[chan []byte]struct{})
		}
		newHub.Mu.Unlock()
	}

	// 将当前缓存的数据包传递给新hub，以提高热切换流畅性
	if len(h.CacheBuffer) > 0 {
		newHub.Mu.Lock()
		// 复制缓存数据到新hub
		newHub.CacheBuffer = make([][]byte, len(h.CacheBuffer))
		copy(newHub.CacheBuffer, h.CacheBuffer)
		newHub.Mu.Unlock()
	}

	// 将所有客户端迁移到新Hub
	clientCount := 0
	for ch := range h.Clients {
		// 添加客户端到新Hub
		newHub.Mu.Lock()
		newHub.Clients[ch] = struct{}{}
		// 发送最新的帧以实现无缝切换
		if len(h.LastFrame) > 0 {
			select {
			case ch <- h.LastFrame:
			default:
			}
		}
		newHub.Mu.Unlock()
		clientCount++
	}

	// 清空当前Hub的客户端列表
	h.Clients = make(map[chan []byte]struct{})

	logger.LogPrintf("🔄 客户端已迁移到新Hub，数量=%d", clientCount)
}

// UpdateInterfaces 更新网络接口配置
func (h *StreamHub) UpdateInterfaces(udpAddr string, ifaces []string) error {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	// 创建新的UDP连接
	addr, err := net.ResolveUDPAddr("udp", udpAddr)
	if err != nil {
		return err
	}

	var newConn *net.UDPConn
	if len(ifaces) == 0 {
		// 未指定网卡，优先多播，再降级普通 UDP
		newConn, err = net.ListenMulticastUDP("udp", nil, addr)
		if err != nil {
			newConn, err = net.ListenUDP("udp", addr)
			if err != nil {
				return err
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
			newConn, err = net.ListenMulticastUDP("udp", iface, addr)
			if err == nil {
				logger.LogPrintf("🟢 监听 %s@%s 成功", udpAddr, name)
				break
			}
			lastErr = err
			logger.LogPrintf("⚠️ 监听 %s@%s 失败: %v", udpAddr, name, err)
		}
		if newConn == nil {
			// 所有网卡失败，尝试普通 UDP
			newConn, err = net.ListenUDP("udp", addr)
			if err != nil {
				return fmt.Errorf("所有网卡监听失败且 UDP 监听失败: %v (last=%v)", err, lastErr)
			}
			logger.LogPrintf("🟡 回退为普通 UDP 监听 %s", udpAddr)
		}
	}

	// 增大内核缓冲区，尽可能减小丢包
	_ = newConn.SetReadBuffer(8 * 1024 * 1024)

	// 关闭旧连接
	if h.UdpConn != nil {
		_ = h.UdpConn.Close()
	}

	// 使用新连接替换旧连接
	h.UdpConn = newConn
	h.addr = udpAddr

	logger.LogPrintf("UDP 监听地址更新：%s ifaces=%v", udpAddr, ifaces)
	return nil
}

// 关闭 hub
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
	if h.UdpConn != nil {
		_ = h.UdpConn.Close()
		h.UdpConn = nil
	}

	// 关闭所有客户端通道
	for ch := range h.Clients {
		close(ch)
	}
	h.Clients = nil

	// 清理缓存数据
	h.CacheBuffer = nil

	logger.LogPrintf("UDP监听已关闭，端口已释放: %s", h.addr)
}

func HubKey(addr string, ifaces []string) string {
	return addr + "|" + strings.Join(ifaces, ",")
}

func GetOrCreateHub(udpAddr string, ifaces []string) (*StreamHub, error) {
	key := HubKey(udpAddr, ifaces)

	HubsMu.Lock()
	defer HubsMu.Unlock()

	// 检查是否已存在对应 key 的 hub
	if hub, ok := Hubs[key]; ok {
		select {
		case <-hub.Closed:
			// 如果 hub 已关闭，则从全局映射中删除
			delete(Hubs, key)
			logger.LogPrintf("🗑️ 删除已关闭的Hub: %s", key)
		default:
			// 如果 hub 仍在运行，直接返回它
			return hub, nil
		}
	}

	// 创建新的 hub
	newHub, err := NewStreamHub(udpAddr, ifaces)
	if err != nil {
		return nil, err
	}

	// 将新的 hub 插入全局映射
	Hubs[key] = newHub
	return newHub, nil
}
