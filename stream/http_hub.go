package stream

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
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

// TS切片缓存项
type TSCacheItem struct {
	Data      []byte
	Timestamp time.Time
}

// HTTPHub 代表一个直播流缓存中心
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
	cacheBytes    int64        // 使用原子操作
	maxCacheBytes int64        // 使用原子操作
	cacheMu       sync.RWMutex // 单独的缓存锁，减少锁争用

	latest   []byte
	latestMu sync.RWMutex

	// TS缓存相关
	tsCache       map[string]*TSCacheItem
	tsCacheMu     sync.RWMutex
	maxTSCacheItems int
	tsCleanupTicker *time.Ticker  // 定期清理过期TS缓存

	m3u8PollCancel context.CancelFunc  // M3U8轮询取消函数
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
		clients:         make(map[*HTTPHubClient]struct{}),
		key:             normalizedKey,
		idleTimeout:     60 * time.Second,
		cache:           make([][]byte, 0),
		cacheBytes:      0,
		maxCacheBytes:   64 << 20, // 64MB 缓存，更适合 4K 视频流
		cacheMu:         sync.RWMutex{},
		tsCache:         make(map[string]*TSCacheItem),
		maxTSCacheItems: 20, // 最多缓存20个TS文件
		latest:          nil,
	}
	
	// 启动TS缓存清理定时器
	h.tsCleanupTicker = time.NewTicker(30 * time.Second) // 每30秒清理一次
	go func() {
		for range h.tsCleanupTicker.C {
			h.cleanupExpiredTSCache()
		}
	}()

	httpHubs[normalizedKey] = h
	return h
}

// URL 标准化
func normalizeHubKey(rawURL string, statusCode int) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL + "#" + string(statusCode)
	}
	u.RawQuery = "" // 去掉 query
	u.Fragment = "" // 去掉 fragment
	return u.String() + "#" + string(statusCode)
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
	if h.tsCleanupTicker != nil {
		h.tsCleanupTicker.Stop()
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

// 广播数据并缓存（优化：客户端阻塞时丢弃当前数据，而非直接移除）
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
		c.ch <- buf // ✅ 阻塞，交给 WriteLoop / TCP
	}
}

// 判断是否为TS请求
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

// 判断是否为M3U8请求
func (h *HTTPHub) IsM3U8Request(rawURL string) bool {
	// 解析URL以获取路径部分（不包含查询参数）
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		// 如果解析失败，回退到原始方法
		ext := strings.ToLower(filepath.Ext(rawURL))
		return ext == ".m3u8"
	}
	
	// 只检查路径部分的扩展名
	ext := strings.ToLower(filepath.Ext(parsedURL.Path))
	return ext == ".m3u8"
}

// 存储TS内容到缓存
func (h *HTTPHub) StoreTS(tsURL string, data []byte) {
	// 使用标准化的URL作为缓存键，与Hub键保持一致
	normalizedURL := normalizeCacheKey(tsURL)
	
	h.tsCacheMu.Lock()
	defer h.tsCacheMu.Unlock()

	// 复制数据以避免底层数据引用问题
	buf := make([]byte, len(data))
	copy(buf, data)

	// 检查是否已存在，存在则更新时间
	if _, exists := h.tsCache[normalizedURL]; exists {
		logger.LogPrintf("更新TS缓存: %s", normalizedURL)
	} else {
		logger.LogPrintf("存储TS到缓存: %s", normalizedURL)
	}
	
	h.tsCache[normalizedURL] = &TSCacheItem{
		Data:      buf,
		Timestamp: time.Now(),
	}

	// 检查缓存项数量，如果超过限制则删除最旧的项
	if len(h.tsCache) > h.maxTSCacheItems {
		h.cleanupOldTSCache()
	}
}

// 从缓存获取TS内容
func (h *HTTPHub) GetTS(tsURL string) ([]byte, bool) {
	// 使用标准化的URL作为缓存键，与Hub键保持一致
	normalizedURL := normalizeCacheKey(tsURL)
	
	h.tsCacheMu.RLock()
	defer h.tsCacheMu.RUnlock()

	if item, exists := h.tsCache[normalizedURL]; exists {
		logger.LogPrintf("从缓存获取TS: %s", normalizedURL)
		// 复制数据返回，避免直接返回内部数据
		buf := make([]byte, len(item.Data))
		copy(buf, item.Data)
		return buf, true
	}

	logger.LogPrintf("TS未在缓存中: %s", normalizedURL)
	return nil, false
}

// 标准化缓存键，与Hub键保持一致
func normalizeCacheKey(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.RawQuery = "" // 去掉 query
	u.Fragment = "" // 去掉 fragment
	return u.String()
}

// 检查并清理过期的TS缓存
func (h *HTTPHub) cleanupExpiredTSCache() {
	h.tsCacheMu.Lock()
	defer h.tsCacheMu.Unlock()

	now := time.Now()
	for key, item := range h.tsCache {
		if now.Sub(item.Timestamp) > 300*time.Second {
			delete(h.tsCache, key)
			logger.LogPrintf("清理过期TS缓存: %s", key)
		}
	}
}

// 清理最旧的TS缓存项，确保不超过最大数量限制
func (h *HTTPHub) cleanupOldTSCache() {
	h.tsCacheMu.Lock()
	defer h.tsCacheMu.Unlock()

	// 创建一个包含缓存项及其时间戳的切片用于排序
	type tsEntry struct {
		key       string
		timestamp time.Time
	}
	entries := make([]tsEntry, 0, len(h.tsCache))
	
	for key, item := range h.tsCache {
		entries = append(entries, tsEntry{key: key, timestamp: item.Timestamp})
	}
	
	// 按时间戳排序（从最旧到最新）
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].timestamp.Before(entries[j].timestamp)
	})
	
	// 删除最旧的项直到缓存数量在限制内
	overhead := len(h.tsCache) - h.maxTSCacheItems
	for i := 0; i < overhead && i < len(entries); i++ {
		delete(h.tsCache, entries[i].key)
		logger.LogPrintf("清理最旧TS缓存: %s", entries[i].key)
	}
}

// 解析M3U8内容并获取TS列表
func (h *HTTPHub) ParseM3U8Content(content []byte) []string {
	var tsList []string
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	
	baseURL := h.getBaseURL()
	
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			// 如果是相对路径，需要转换为绝对路径
			if strings.HasPrefix(line, "http") {
				tsList = append(tsList, line)
			} else if baseURL != "" {
				tsList = append(tsList, baseURL+"/"+line)
			} else {
				tsList = append(tsList, line)
			}
		}
	}
	
	return tsList
}

// 获取基础URL
func (h *HTTPHub) getBaseURL() string {
	parsedURL, err := url.Parse(h.key)
	if err != nil {
		return ""
	}
	
	parsedURL.Path = filepath.Dir(parsedURL.Path)
	return parsedURL.String()
}

// 预下载TS文件
func (h *HTTPHub) PreDownloadTS(tsURLs []string) {
	for _, tsURL := range tsURLs {
		// 检查是否已存在缓存
		if _, exists := h.GetTS(tsURL); !exists {
			// 启动goroutine下载TS文件
			go func(url string) {
				resp, err := http.Get(url)
				if err != nil {
					logger.LogPrintf("下载TS文件失败: %s, 错误: %v", url, err)
					return
				}
				defer resp.Body.Close()
				
				data, err := io.ReadAll(resp.Body)
				if err != nil {
					logger.LogPrintf("读取TS文件失败: %s, 错误: %v", url, err)
					return
				}
				
				h.StoreTS(url, data)
				logger.LogPrintf("预下载TS文件成功: %s", url)
			}(tsURL)
		}
	}
}

// 确保生产者启动
func (h *HTTPHub) EnsureProducer(ctx context.Context, src io.Reader, buf []byte, rawURL string) {
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

	// 根据URL类型决定处理方式
	ext := strings.ToLower(filepath.Ext(rawURL))
	if ext == ".m3u8" {
		// M3U8文件特殊处理：作为长连接流处理，但缓存时间很短
		h.handleM3U8Stream(pCtx, src, buf, rawURL)
	} else if ext == ".ts" {
		// TS文件：直接处理并缓存
		h.handleTSStream(pCtx, src, buf, rawURL)
	} else {
		// 其他类型的流：普通处理
		h.handleGeneralStream(pCtx, src, buf)
	}
}

// 处理M3U8流
func (h *HTTPHub) handleM3U8Stream(ctx context.Context, src io.Reader, buf []byte, m3u8URL string) {
	// 创建M3U8轮询的context
	m3u8PollCtx, pollCancel := context.WithCancel(context.Background())
	h.mu.Lock()
	h.m3u8PollCancel = pollCancel
	h.mu.Unlock()

	go func() {
		defer func() {
			h.mu.Lock()
			h.producerRunning = false
			clientsCount := len(h.clients)
			h.mu.Unlock()
			if clientsCount == 0 {
				h.scheduleIdleClose()
			}
		}()

		// 读取M3U8内容
		var content []byte
		for {
			n, err := src.Read(buf)
			if n > 0 {
				content = append(content, buf[:n]...)
			}
			if err != nil {
				break
			}
			select {
			case <-ctx.Done():
				return
			default:
			}
		}

		// 解析M3U8并预下载TS
		tsURLs := h.ParseM3U8Content(content)
		h.PreDownloadTS(tsURLs)

		// 发送M3U8内容给客户端
		h.Broadcast(content)

		// 启动M3U8轮询goroutine，定期获取新的M3U8内容
		go func() {
			ticker := time.NewTicker(10 * time.Second) // 每10秒轮询一次
			defer ticker.Stop()

			for {
				select {
				case <-ticker.C:
					// 重新获取M3U8内容
					resp, err := http.Get(m3u8URL)
					if err != nil {
						logger.LogPrintf("轮询M3U8失败: %v", err)
						continue
					}
					defer resp.Body.Close()

					newContent, err := io.ReadAll(resp.Body)
					if err != nil {
						logger.LogPrintf("读取轮询M3U8失败: %v", err)
						continue
					}

					// 解析新的TS URL并预下载
					newTSURLs := h.ParseM3U8Content(newContent)
					h.PreDownloadTS(newTSURLs)

					// 发送新内容给客户端
					h.Broadcast(newContent)

				case <-m3u8PollCtx.Done():
					return
				}
			}
		}()
	}()
}

// 处理TS流
func (h *HTTPHub) handleTSStream(ctx context.Context, src io.Reader, buf []byte, tsURL string) {
	go func() {
		defer func() {
			h.mu.Lock()
			h.producerRunning = false
			clientsCount := len(h.clients)
			h.mu.Unlock()
			if clientsCount == 0 {
				h.scheduleIdleClose()
			}
		}()

		// 读取整个TS文件内容
		var content []byte
		for {
			n, err := src.Read(buf)
			if n > 0 {
				content = append(content, buf[:n]...)
			}
			if err != nil {
				break
			}
			select {
			case <-ctx.Done():
				return
			default:
			}
		}

		// 存储到TS缓存 - 在发送给客户端之前先缓存
		h.StoreTS(tsURL, content)
		logger.LogPrintf("TS数据已存储到缓存: %s", tsURL)

		// 如果有客户端，直接发送给它们
		h.mu.Lock()
		clients := make([]*HTTPHubClient, 0, len(h.clients))
		for c := range h.clients {
			clients = append(clients, c)
		}
		h.mu.Unlock()

		for _, c := range clients {
			c.ch <- content
		}
	}()
}

// 处理一般流（非M3U8和TS）
func (h *HTTPHub) handleGeneralStream(ctx context.Context, src io.Reader, buf []byte) {
	go func() {
		defer func() {
			h.mu.Lock()
			h.producerRunning = false
			clientsCount := len(h.clients)
			h.mu.Unlock()
			if clientsCount == 0 {
				h.scheduleIdleClose()
			}
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
			case <-ctx.Done():
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

				// flush 也算"仍在服务中"
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