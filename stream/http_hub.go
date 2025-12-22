package stream

import (
	"context"
	"github.com/qist/tvgate/logger"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
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
	cacheBytes    int
	maxCacheBytes int // 最大缓存总字节数
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
		//logger.LogPrintf("复用已存在的hub: %s (原始key: %s)", normalizedKey, key)
		return h
	}
	// logger.LogPrintf("创建新的hub: %s (原始key: %s)", normalizedKey, key)
	h := &HTTPHub{
		clients:       make(map[*HTTPHubClient]struct{}),
		key:           normalizedKey,
		idleTimeout:   30 * time.Second,
		cache:         make([][]byte, 0),
		cacheBytes:    0,
		maxCacheBytes: 16 << 20, // 16MB 缓存，更适合 4K 视频流
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
	u.RawQuery = ""  // 去掉 query
	u.Fragment = ""  // 去掉 fragment
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

	// 发送缓存给新客户端
	for _, cached := range h.cache {
		select {
		case c.ch <- cached:
		default:
		}
	}
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
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}

	buf := make([]byte, len(data))
	copy(buf, data)

	// 添加到缓存
	h.cache = append(h.cache, buf)
	h.cacheBytes += len(buf)

	// 超出最大字节数时，从头部删除最旧块
	for h.cacheBytes > h.maxCacheBytes && len(h.cache) > 0 {
		h.cacheBytes -= len(h.cache[0])
		h.cache = h.cache[1:]
	}

	clients := make([]*HTTPHubClient, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()

	// 发送给客户端
	for _, c := range clients {
		select {
		case c.ch <- buf:
			// 正常发送
		default:
			// 缓冲满了，丢弃当前数据，不直接移除客户端
			logger.LogPrintf("客户端缓冲满，丢弃当前数据 (Hub: %s)", h.key)
		}
	}
}


// 确保生产者启动
func (h *HTTPHub) EnsureProducer(ctx context.Context, src io.Reader, buf []byte) {
	h.mu.Lock()
	if h.closed || h.producerRunning {
		h.mu.Unlock()
		return
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
				RemoveHTTPHub(h.key)
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
func (c *HTTPHubClient) WriteLoop(ctx context.Context, updateActive func()) error {
	var bytesWritten int
	lastFlush := time.Now()

	flushTicker := time.NewTicker(50 * time.Millisecond) // 高频轮询，动态 flush
	defer flushTicker.Stop()

	activeTicker := (*time.Ticker)(nil)
	if updateActive != nil {
		activeTicker = time.NewTicker(5 * time.Second)
		defer activeTicker.Stop()
		defer updateActive()
	}

	for {
		select {
		case data, ok := <-c.ch:
			if !ok {
				return nil
			}
			written := 0
			for written < len(data) {
				wn, err := c.w.Write(data[written:])
				if err != nil {
					return err
				}
				written += wn
				bytesWritten += wn
			}
		case <-flushTicker.C:
			if c.canFlush && (bytesWritten >= 32*1024 || time.Since(lastFlush) >= 200*time.Millisecond) {
				c.flusher.Flush()
				bytesWritten = 0
				lastFlush = time.Now()
			}
		case <-ctx.Done():
			return ctx.Err()
		case <-activeTicker.C:
			if updateActive != nil {
				updateActive()
			}
		}
	}
}
