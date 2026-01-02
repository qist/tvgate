package stream

import (
	"context"
	"io"
	"net/http"
	"net/url"
	// "path/filepath"
	"strconv"
	// "strings"
	"sync"
	// "sync/atomic"
	"github.com/qist/tvgate/logger"
	"time"
)

type HTTPHubClient struct {
	ch       chan []byte
	w        http.ResponseWriter
	flusher  http.Flusher
	canFlush bool

	mu     sync.Mutex
	closed bool
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
}

var (
	httpHubsMu sync.Mutex
	httpHubs   = make(map[string]*HTTPHub)
)

func GetOrCreateHTTPHub(rawURL string, statusCode int) *HTTPHub {
	normalizedKey := normalizeHubKey(rawURL, statusCode)
	httpHubsMu.Lock()
	defer httpHubsMu.Unlock()
	if h, ok := httpHubs[normalizedKey]; ok {
		logger.LogPrintf("复用已存在的hub: %s (原始URL: %s, 状态码: %d)", normalizedKey, rawURL, statusCode)
		return h
	}
	logger.LogPrintf("创建新的hub: %s (原始URL: %s, 状态码: %d)", normalizedKey, rawURL, statusCode)
	h := &HTTPHub{
		clients:     make(map[*HTTPHubClient]struct{}),
		key:         normalizedKey,
		idleTimeout: 60 * time.Second,
	}
	httpHubs[normalizedKey] = h
	return h
}

func normalizeHubKey(rawURL string, statusCode int) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL + "#" + strconv.Itoa(statusCode)
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String() + "#" + strconv.Itoa(statusCode)
}

func RemoveHTTPHub(key string) {
	httpHubsMu.Lock()
	defer httpHubsMu.Unlock()
	if h, ok := httpHubs[key]; ok {
		h.Close()
		delete(httpHubs, key)
	}
}

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

func (h *HTTPHub) AddClient(w http.ResponseWriter, bufSize int) *HTTPHubClient {
	c := &HTTPHubClient{
		ch: make(chan []byte, bufSize),
		w:  w,
	}
	if f, ok := w.(http.Flusher); ok {
		c.flusher = f
		c.canFlush = true
	}
	h.mu.Lock()
	defer h.mu.Unlock()  // 使用defer确保解锁
	
	if h.closed {
		// 如果hub已关闭，仍然返回client，但客户端会在WriteLoop中检测到并退出
		return c
	}
	if h.idleTimer != nil {
		h.idleTimer.Stop()
		h.idleTimer = nil
	}
	h.clients[c] = struct{}{}
	return c
}

func (h *HTTPHub) RemoveClient(c *HTTPHubClient) {
	h.mu.Lock()
	defer h.mu.Unlock()  // 使用defer确保解锁
	
	if !h.closed {  // 只有在hub未关闭时才删除客户端
		delete(h.clients, c)
	}
	empty := len(h.clients) == 0 && !h.closed  // 确保只有在hub未关闭时才调度清理
	h.mu.Unlock()
	
	c.safeClose()
	
	if empty {
		h.scheduleIdleClose()
	}
}

func (h *HTTPHub) Broadcast(data []byte) {
	buf := make([]byte, len(data))
	copy(buf, data)
	h.mu.Lock()
	clients := make([]*HTTPHubClient, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()
	for _, c := range clients {
		select {
		case c.ch <- buf:
		default:
			logger.LogPrintf("hub %s: 移除慢客户端 %p", h.key, c)
			h.RemoveClient(c) // 阻塞保护
		}
	}
}

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
				data := make([]byte, n)
				copy(data, buf[:n])
				h.Broadcast(data)
			}
			if err != nil {
				h.scheduleIdleClose()
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

func (c *HTTPHubClient) WriteLoop(ctx context.Context, updateActive func()) error {
	const (
		flushInterval = 100 * time.Millisecond
		maxFlushBytes = 32 * 1024
		maxFlushDelay = 200 * time.Millisecond
	)
	var (
		lastFlush    = time.Now()
		bytesWritten = 0
	)
	flushTicker := time.NewTicker(flushInterval)
	defer flushTicker.Stop()
	
	for {
		select {
		case data, ok := <-c.ch:
			if !ok {
				// 通道已关闭，退出循环
				return nil
			}
			written := 0
			for written < len(data) {
				n, err := c.w.Write(data[written:])
				if err != nil {
					return err
				}
				written += n
				bytesWritten += n
			}
			if updateActive != nil {
				updateActive()
			}
		case <-flushTicker.C:
			c.mu.Lock()  // 在访问flusher前加锁
			if c.canFlush && bytesWritten > 0 &&
				(bytesWritten >= maxFlushBytes || time.Since(lastFlush) >= maxFlushDelay) {
				c.flusher.Flush()
				bytesWritten = 0
				lastFlush = time.Now()
				if updateActive != nil {
					updateActive()
				}
			}
			c.mu.Unlock()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

