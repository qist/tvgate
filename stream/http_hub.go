package stream

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
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

	// 慢客户端控制
	slowCount int
	lastSlow  time.Time
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

// 获取或创建 Hub
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

// URL 标准化
func normalizeHubKey(rawURL string, statusCode int) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL + "#" + strconv.Itoa(statusCode)
	}
	u.RawQuery = "" // 去掉 query
	u.Fragment = "" // 去掉 fragment
	return u.String() + "#" + strconv.Itoa(statusCode)
}

func (h *HTTPHub) IsTSRequest(rawURL string) bool {
	// 解析URL以获取路径部分（不包含查询参数）
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		// 如果解析失败，回退到原始方法
		ext := strings.ToLower(filepath.Ext(rawURL))
		return ext == ".ts"
	}

	// 只检查路径部分的扩展名
	ext := strings.ToLower(filepath.Ext(parsedURL.Path))
	return ext == ".ts"
}

func normalizeCacheKey(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.RawQuery = ""
	u.Fragment = ""
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
		ch: make(chan []byte, bufSize),
		w:  w,
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
	h.mu.Unlock()

	// ❗ 不 replay cache，新 client 只接实时流
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

// 广播数据并缓存
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
			// 成功写入
		default:
			logger.LogPrintf("hub %s: 移除慢客户端 %p", h.key, c)
			h.RemoveClient(c) // 阻塞保护
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
		maxFlushDelay = 200 * time.Millisecond
	)

	var (
		lastDataAt   = time.Now()
		lastFlush    = time.Now()
		bytesWritten = 0
	)

	flushTicker := time.NewTicker(flushInterval)
	idleTicker := time.NewTicker(500 * time.Millisecond)
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
			if c.canFlush && bytesWritten > 0 &&
				(bytesWritten >= maxFlushBytes ||
					time.Since(lastFlush) >= maxFlushDelay) {

				c.flusher.Flush()
				bytesWritten = 0
				lastFlush = time.Now()

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


func StreamTS(backendKey string, src io.Reader, dst io.Writer) error {
	key := normalizeCacheKey(backendKey)

	return GlobalTSCache.FetchOrGetStream(key, dst, func(w io.Writer) error {
		buf := make([]byte, 32*1024) // 32KB 分片
		for {
			n, err := src.Read(buf)
			if n > 0 {
				chunk := buf[:n]
				if _, wErr := w.Write(chunk); wErr != nil {
					return wErr
				}

				// 如果 dst 支持 http.Flusher，立即 flush
				if f, ok := dst.(http.Flusher); ok {
					f.Flush()
				}
			}

			if err != nil {
				if err == io.EOF {
					return nil
				}
				return err
			}
		}
	})
}