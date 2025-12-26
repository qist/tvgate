package stream

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qist/tvgate/logger"
)

type HTTPHubClient struct {
	ch       chan []byte
	w        http.ResponseWriter
	flusher  http.Flusher
	canFlush bool
	mu       sync.Mutex
	closed   bool
}

type HTTPHub struct {
	mu               sync.Mutex
	clients          map[*HTTPHubClient]struct{}
	closed           bool
	producerRunning  bool
	producerCancelFn context.CancelFunc
	key              string
	idleTimer        *time.Timer
	idleTimeout      time.Duration

	// 缓存相关
	cache         [][]byte
	cacheBytes    int64  // 使用原子操作
	maxCacheBytes int64  // 使用原子操作
	cacheMu       sync.RWMutex  // 单独的缓存锁，减少锁争用
}

var (
	httpHubsMu sync.Mutex
	httpHubs   = make(map[string]*HTTPHub)
)

// 获取或创建 Hub
func GetOrCreateHTTPHub(key string) *HTTPHub {
	normalizedKey := normalizeHubKey(key)

	httpHubsMu.Lock()
	defer httpHubsMu.Unlock()
	if h, ok := httpHubs[normalizedKey]; ok {
		// logger.LogPrintf("复用已存在的hub: %s (原始key: %s)", normalizedKey, key)
		return h
	}
	// logger.LogPrintf("创建新的hub: %s (原始key: %s)", normalizedKey, key)
	h := &HTTPHub{
		clients:       make(map[*HTTPHubClient]struct{}),
		key:           normalizedKey,
		idleTimeout:   60 * time.Second,
		cache:         make([][]byte, 0),
		cacheBytes:    0,
		maxCacheBytes: 64 << 20, // 64MB 缓存，更适合 4K 视频流
		cacheMu:       sync.RWMutex{},
	}
	httpHubs[normalizedKey] = h
	return h
}

// URL 标准化
func normalizeHubKey(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.RawQuery = "" // 去掉 query
	u.Fragment = "" // 去掉 fragment
	return u.String()
}

// 删除 Hub
func RemoveHTTPHub(key string) {
	httpHubsMu.Lock()
	defer httpHubsMu.Unlock()
	if h, ok := httpHubs[key]; ok {
		if !h.closed {
			h.Close()
		}
		delete(httpHubs, key)
	}
}

// 关闭 Hub
func (h *HTTPHub) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	if h.idleTimer != nil {
		h.idleTimer.Stop()
		h.idleTimer = nil
	}
	if h.producerCancelFn != nil {
		h.producerCancelFn()
	}
	clients := make([]*HTTPHubClient, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.clients = nil
	h.mu.Unlock()

	for _, c := range clients {
		c.safeClose()
	}
}

// 添加客户端
func (h *HTTPHub) AddClient(w http.ResponseWriter, bufSize int) *HTTPHubClient {
	c := &HTTPHubClient{
		ch:       make(chan []byte, bufSize),
		w:        w,
		flusher:  nil,
		canFlush: false,
	}
	if f, ok := w.(http.Flusher); ok {
		c.flusher = f
		c.canFlush = true
	}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return c
	}
	if h.idleTimer != nil {
		h.idleTimer.Stop()
		h.idleTimer = nil
	}
	h.clients[c] = struct{}{}

	// 读取缓存数据并发送给新客户端
	h.cacheMu.RLock()
	for _, cached := range h.cache {
		if !h.trySend(c, cached) {
			break
		}
	}
	h.cacheMu.RUnlock()
	h.mu.Unlock()
	return c
}

// 移除客户端
func (h *HTTPHub) RemoveClient(c *HTTPHubClient) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
	}
	empty := !h.closed && len(h.clients) == 0
	h.mu.Unlock()

	c.safeClose()

	if empty {
		h.scheduleIdleClose()
	}
}

// 广播数据并缓存（优化：客户端阻塞时丢弃当前数据，而非直接移除）
func (h *HTTPHub) Broadcast(data []byte) {
	// 创建数据副本
	buf := make([]byte, len(data))
	copy(buf, data)

	// 添加到缓存
	h.cacheMu.Lock()
	h.cache = append(h.cache, buf)
	newCacheBytes := atomic.AddInt64(&h.cacheBytes, int64(len(buf)))

	// 超出最大字节数时，从头部删除最旧块
	for newCacheBytes > atomic.LoadInt64(&h.cacheBytes) && len(h.cache) > 0 {
		removed := h.cache[0]
		h.cache = h.cache[1:]
		atomic.AddInt64(&h.cacheBytes, -int64(len(removed)))
		newCacheBytes = atomic.LoadInt64(&h.cacheBytes)
	}
	h.cacheMu.Unlock()

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}

	clients := make([]*HTTPHubClient, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()

	for _, c := range clients {
		if !h.trySend(c, buf) {
			logger.LogPrintf("客户端缓冲无法接收，丢弃数据 (Hub: %s)", h.key)
		}
	}
}

// 确保生产者启动
func (h *HTTPHub) EnsureProducer(ctx context.Context, src io.Reader, buf []byte) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	if h.producerRunning && h.producerCancelFn != nil {
		h.producerCancelFn()
	}
	h.producerRunning = true
	pCtx, cancel := context.WithCancel(ctx)
	h.producerCancelFn = cancel
	h.mu.Unlock()

	go func() {
		defer func() {
			h.mu.Lock()
			h.producerRunning = false
			h.mu.Unlock()
		}()
		for {
			n, err := src.Read(buf)
			if n > 0 {
				h.Broadcast(buf[:n])
			}
			if err != nil {
				h.mu.Lock()
				h.producerRunning = false
				clientsCount := len(h.clients)
				h.mu.Unlock()
				if clientsCount == 0 {
					h.scheduleIdleClose()
				}
				return
			}
			select {
			case <-pCtx.Done():
				return
			default:
			}
		}
	}()
}

// 空闲关闭
func (h *HTTPHub) scheduleIdleClose() {
	h.mu.Lock()
	if h.closed || h.idleTimer != nil {
		h.mu.Unlock()
		return
	}
	timeout := h.idleTimeout
	h.idleTimer = time.AfterFunc(timeout, func() {
		h.mu.Lock()
		isEmpty := len(h.clients) == 0
		h.mu.Unlock()
		if isEmpty {
			RemoveHTTPHub(h.key)
		}
	})
	h.mu.Unlock()
}

func (h *HTTPHub) trySend(c *HTTPHubClient, data []byte) bool {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return false
	}
	ok := true
	select {
	case c.ch <- data:
	default:
		ok = false
	}
	c.mu.Unlock()
	return ok
}

// 客户端安全关闭
func (c *HTTPHubClient) safeClose() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	close(c.ch)
	c.mu.Unlock()
}

// 客户端写循环（优化：动态 flush，时间+字节双条件）
func (c *HTTPHubClient) WriteLoop(
	ctx context.Context,
	updateActive func(),
) error {

	const (
		tsIdleTimeout = 6 * time.Second
		flushInterval = 100 * time.Millisecond
		maxFlushBytes = 32 * 1024
	)

	var (
		lastDataAt   = time.Now()
		bytesWritten = 0
	)

	flushTicker := time.NewTicker(flushInterval)
	idleTicker  := time.NewTicker(500 * time.Millisecond)
	defer flushTicker.Stop()
	defer idleTicker.Stop()

	// 只要 client 生命周期内，hub 就算活跃
	if updateActive != nil {
		updateActive()
	}

	for {
		select {

		case data, ok := <-c.ch:
			if !ok {
				return nil
			}

			lastDataAt = time.Now()

			written := 0
			for written < len(data) {
				n, err := c.w.Write(data[written:])
				if err != nil {
					return err
				}
				written += n
				bytesWritten += n
			}

			// 写到数据，说明流真的在走
			if updateActive != nil {
				updateActive()
			}

		case <-flushTicker.C:
			if c.canFlush && bytesWritten >= maxFlushBytes {
				c.flusher.Flush()
				bytesWritten = 0

				// flush 也算“仍在服务中”
				if updateActive != nil {
					updateActive()
				}
			}

		case <-idleTicker.C:
			// TS 无数据，但在 idle timeout 内
			// 仍然认为 hub 是活跃的
			if updateActive != nil {
				updateActive()
			}

			if time.Since(lastDataAt) > tsIdleTimeout {
				// 只结束 TS，不影响 hub
				return io.EOF
			}

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
