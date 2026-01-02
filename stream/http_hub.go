package stream

import (
	"context"
	"io"
	"net/http"
	"net/url"
	// "path/filepath"
	// "strconv"
	"strings"
	"sync"
	// "sync/atomic"
	"github.com/qist/tvgate/logger"
	"time"
	"github.com/qist/tvgate/utils/buffer/ringbuffer"
)

type HTTPHubClient struct {
	ch       *ringbuffer.RingBuffer
	w        http.ResponseWriter
	flusher  http.Flusher
	canFlush bool

	mu     sync.Mutex
	closed bool
	
	// 添加客户端是否已发送头部信息的标记
	hasReceivedHeader bool
}

type HTTPHub struct {
	mu               sync.Mutex
	clients          map[*HTTPHubClient]struct{}
	closed           bool
	producerRunning  bool
	producerCancelFn context.CancelFunc
	key              string
	
	// 添加状态管理
	state       int // 0: stopped, 1: playing, 2: error
	stateCond   *sync.Cond
	lastError   error
	maxRetryAttempts int
	retryCount       int
	
	// 添加FLV头部信息缓存
	flvHeader        []byte
	videoConfig      []byte
	audioConfig      []byte
}

var (
	httpHubsMu sync.RWMutex  // 使用读写锁提高并发性能
	httpHubs   = make(map[string]*HTTPHub)
)

func GetOrCreateHTTPHub(rawURL string) *HTTPHub {
	normalizedKey := normalizeHubKey(rawURL)
	
	httpHubsMu.RLock()
	// 先尝试读取已存在的hub
	if h, ok := httpHubs[normalizedKey]; ok {
		// 检查hub是否已经关闭
		h.mu.Lock()
		isClosed := h.closed
		h.mu.Unlock()
		
		if !isClosed {
			httpHubsMu.RUnlock()
			logger.LogPrintf("复用已存在的hub: %s (原始URL: %s)", normalizedKey, rawURL)
			return h
		}
		// 如果已关闭，释放读锁，获取写锁来移除旧的hub
		httpHubsMu.RUnlock()
		httpHubsMu.Lock()
		// 双重检查，确保没有其他goroutine已经创建了新的hub
		if h, exists := httpHubs[normalizedKey]; exists {
			h.Close() // 确保hub被正确关闭
			delete(httpHubs, normalizedKey)
		}
		httpHubsMu.Unlock()
	} else {
		httpHubsMu.RUnlock()
	}

	// 获取写锁来创建新的hub
	httpHubsMu.Lock()
	defer httpHubsMu.Unlock()

	// 再次检查是否已经有其他goroutine创建了hub
	if h, exists := httpHubs[normalizedKey]; exists {
		// 检查这个hub是否已经关闭
		h.mu.Lock()
		isClosed := h.closed
		h.mu.Unlock()
		
		if !isClosed {
			logger.LogPrintf("复用已存在的hub: %s (原始URL: %s)", normalizedKey, rawURL)
			return h
		}
		// 如果已关闭，移除并创建新的
		h.Close()
		delete(httpHubs, normalizedKey)
	}

	logger.LogPrintf("创建新的hub: %s (原始URL: %s)", normalizedKey, rawURL)
	h := &HTTPHub{
		clients:          make(map[*HTTPHubClient]struct{}),
		key:              normalizedKey,
		maxRetryAttempts: 10, // 设置最大重试次数
		retryCount:       0,
	}
	h.stateCond = sync.NewCond(&h.mu)
	httpHubs[normalizedKey] = h
	return h
}

func normalizeHubKey(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
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
	defer h.mu.Unlock()
	
	if h.closed {
		return
	}
	h.closed = true
	h.state = StateStopped
	h.stateCond.Broadcast() // 通知所有等待状态的goroutine
	
	if h.producerCancelFn != nil {
		h.producerCancelFn()
	}
	clients := make([]*HTTPHubClient, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.clients = nil
	
	for _, c := range clients {
		c.safeClose()
	}
}

func (h *HTTPHub) AddClient(w http.ResponseWriter, bufSize int) *HTTPHubClient {
	rb := createRingBuffer(bufSize) // 为每个客户端创建独立的ringbuffer
	
	c := &HTTPHubClient{
		ch: rb,
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
	h.clients[c] = struct{}{}
	return c
}

// createRingBuffer 创建ringbuffer的辅助函数
func createRingBuffer(size int) *ringbuffer.RingBuffer {
	// 确保大小是2的幂次方
	ringSize := 4096 // 默认大小
	if size > 0 {
		// 找到大于等于size的最小2的幂次方
		for ringSize < size {
			ringSize <<= 1
		}
	}
	rb, err := ringbuffer.New(uint64(ringSize))
	if err != nil {
		// 如果指定大小不是2的幂次方，使用默认大小
		rb, _ = ringbuffer.New(4096)
	}
	return rb
}

func (h *HTTPHub) RemoveClient(c *HTTPHubClient) {
	h.mu.Lock()
	if !h.closed && h.clients != nil {
		delete(h.clients, c)
	}
	clientCount := len(h.clients)
	h.mu.Unlock()
	
	c.safeClose()
	
	// 如果没有客户端了，关闭Hub
	if clientCount == 0 {
		RemoveHTTPHub(h.key) // 使用全局函数来移除并清理Hub
	}
}

func (h *HTTPHub) Broadcast(data []byte) {
	h.mu.Lock()
	clients := make([]*HTTPHubClient, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()
	
	for _, c := range clients {
		buf := make([]byte, len(data))
		copy(buf, data) // 复制数据以避免引用问题
		
		if !c.ch.Push(buf) {
			// 如果推送失败，可能是通道已关闭，从客户端列表中移除
			h.removeClientIfNotExist(c)
		}
	}
}


// removeClientIfNotExist 从客户端列表中移除已不存在的客户端
func (h *HTTPHub) removeClientIfNotExist(c *HTTPHubClient) {
	h.mu.Lock()
	// 再次确认客户端是否还在列表中
	if _, exists := h.clients[c]; exists {
		delete(h.clients, c)
		h.mu.Unlock() // 在调用 safeClose 前先释放锁
		c.safeClose()
		return
	}
	h.mu.Unlock() // 如果客户端不存在，也需要确保解锁
}

func (h *HTTPHub) ClientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

func (h *HTTPHub) EnsureProducer(ctx context.Context, src io.Reader, buf []byte) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	
	// 检查是否已经有producer在运行，如果有则不启动新的
	if h.producerRunning {
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
		
		// 设置为播放状态
		h.SetPlaying()
		
		for {
			n, err := src.Read(buf)
			if n > 0 {
				// 检查是否是FLV流，只有FLV流才缓存头部信息
				if isFLVStream(h.key) {
					// 检查是否是头部信息并缓存
					data := buf[:n]
					
					// 检查是否是FLV头部或配置信息并缓存
					if isFLVHeader(data) && h.flvHeader == nil {
						h.flvHeader = make([]byte, n)
						copy(h.flvHeader, data)
					} else if isVideoConfig(data) && h.videoConfig == nil {
						h.videoConfig = make([]byte, n)
						copy(h.videoConfig, data)
					} else if isAudioConfig(data) && h.audioConfig == nil {
						h.audioConfig = make([]byte, n)
						copy(h.audioConfig, data)
					}
				}
				
				h.Broadcast(buf[:n])
			}
			if err != nil {
				logger.LogPrintf("Hub producer error: %v for hub %s", err, h.key)
				
				// 设置错误状态
				h.SetError(err)
				
				// 尝试重连
				if h.shouldRetry() {
					// logger.LogPrintf("Hub %s attempting to reconnect... (attempt %d/%d)", h.key, h.retryCount, h.maxRetryAttempts)
					time.Sleep(5 * time.Second) // 等待5秒后重试
                    
					h.SetStopped() // 重试前设置为停止状态
					h.retryCount++
					return // 退出当前goroutine，让新的EnsureProducer被调用
				} else {
					// logger.LogPrintf("Hub %s reached max retry attempts, closing hub", h.key)
					// 不再使用scheduleIdleClose，直接关闭hub
					h.Close()
					return
				}
			}
			select {
			case <-pCtx.Done():
				return
			default:
			}
		}
	}()
}


func (h *HTTPHub) shouldRetry() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.retryCount < h.maxRetryAttempts && !h.closed
}


func (c *HTTPHubClient) safeClose() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.ch.Close()
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
	
	// 等待Hub进入播放状态
	if !c.waitForPlaying(ctx) {
		return ctx.Err()
	}
	
	// 获取客户端所属的Hub并发送缓存的头部信息（仅对FLV流）
	hub := c.getHubByClient()
	if hub != nil {
		// 检查是否为FLV流
		if isFLVStream(hub.key) {
			hub.mu.Lock()
			// 发送缓存的头部信息（如果存在）
			if hub.flvHeader != nil {
				// 发送FLV头部
				c.sendToClient(hub.flvHeader)
			}
			if hub.videoConfig != nil {
				// 发送视频配置信息
				c.sendToClient(hub.videoConfig)
			}
			if hub.audioConfig != nil {
				// 发送音频配置信息
				c.sendToClient(hub.audioConfig)
			}
			hub.mu.Unlock()
		}
	}
	
	for {
		select {
		case data, ok := <-c.ch.Chan(): // 从ringbuffer的channel读取数据
			if !ok {
				// 通道已关闭，退出循环
				return nil
			}
			
			// 进行类型断言
			byteData, ok := data.([]byte)
			if !ok {
				continue // 如果类型断言失败，跳过此次循环
			}
			
			written := 0
			for written < len(byteData) {
				n, err := c.w.Write(byteData[written:])
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

// sendToClient 发送数据到客户端
func (c *HTTPHubClient) sendToClient(data []byte) {
	if len(data) == 0 {
		return
	}
	
	// 立即发送数据到客户端
	_, err := c.w.Write(data)
	if err != nil {
		logger.LogPrintf("发送缓存头部数据到客户端失败: %v", err)
		return
	}
	
	// 如果支持flush，立即发送
	if c.canFlush {
		c.flusher.Flush()
	}
}

// waitForPlaying 等待Hub进入播放状态
func (c *HTTPHubClient) waitForPlaying(ctx context.Context) bool {
	hub := c.getHubByClient()
	if hub == nil {
		return true // 如果找不到hub，直接返回
	}
	
	return hub.WaitForPlaying(ctx)
}

// getHubByClient 通过客户端查找对应的Hub
func (c *HTTPHubClient) getHubByClient() *HTTPHub {
	httpHubsMu.RLock()
	defer httpHubsMu.RUnlock()
	
	for _, hub := range httpHubs {
		hub.mu.Lock()
		_, exists := hub.clients[c]
		hub.mu.Unlock()
		if exists {
			return hub
		}
	}
	return nil
}

// 新增方法：设置流为播放状态
func (h *HTTPHub) SetPlaying() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.state = StatePlaying
	h.lastError = nil
	h.retryCount = 0 // 重置重试计数
	h.stateCond.Broadcast()
}

// 新增方法：设置流为停止状态
func (h *HTTPHub) SetStopped() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.state = StateStopped
	h.stateCond.Broadcast()
}

// 新增方法：设置流为错误状态
func (h *HTTPHub) SetError(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.state = StateError
	h.lastError = err
	h.stateCond.Broadcast()
}

// 新增方法：获取最后的错误
func (h *HTTPHub) GetLastError() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastError
}

// 新增方法：等待流变为播放状态
func (h *HTTPHub) WaitForPlaying(ctx context.Context) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	// 如果已经关闭，直接返回
	if h.closed {
		return false
	}
	
	// 如果在错误状态，返回错误
	if h.state == StateError {
		return false
	}
	
	// 如果已经在播放，直接返回
	if h.state == StatePlaying {
		return true
	}
	
	// 等待状态变化或context取消
	for h.state == StateStopped && !h.closed && ctx.Err() == nil {
		h.stateCond.Wait() // 这会自动释放锁并在唤醒时重新获取锁
	}
	
	// 检查context是否已取消
	if ctx.Err() != nil {
		return false
	}
	
	// 检查最终状态
	if h.state == StateError {
		return false
	}
	return !h.closed && h.state == StatePlaying
}


// isFLVStream 根据路径判断是否为FLV流
func isFLVStream(key string) bool {
	// 从hub key中提取URL部分（去掉状态码）
	parts := strings.Split(key, "#")
	if len(parts) < 2 {
		// 如果格式不正确，直接使用整个key作为URL
		return isFLVStreamPathOnly(key)
	}
	
	// 重建URL部分（去掉最后的#和状态码）
	urlPart := strings.Join(parts[:len(parts)-1], "#")
	return isFLVStreamPathOnly(urlPart)
}

// isFLVStreamPathOnly 检查URL路径是否为FLV流
func isFLVStreamPathOnly(path string) bool {
	p := strings.ToLower(path)

	// 解析URL以正确提取路径部分，去除查询参数和片段
	if u, err := url.Parse(p); err == nil {
		p = strings.ToLower(u.Path)
	}

	// 检查是否为FLV文件
	return strings.HasSuffix(p, ".flv")
}

// isFLVHeader 检查数据是否为FLV头部
func isFLVHeader(data []byte) bool {
	if len(data) < 9 {
		return false
	}
	// FLV header: "FLV" + version(0x01) + flags + offset(0x00000009)
	return data[0] == 0x46 && data[1] == 0x4C && data[2] == 0x56 &&  // "FLV"
		   data[3] == 0x01 &&  // version
		   data[4] & 0xFA == 0 && // flags (bit 6,7,8 should be 0)
		   data[5] == 0x00 && data[6] == 0x00 && data[7] == 0x00 && data[8] == 0x09 // offset
}

// isVideoConfig 检查数据是否为视频配置信息 (AVCDecoderConfigurationRecord)
func isVideoConfig(data []byte) bool {
	// 检查是否为AVC sequence header
	// 格式：[AVC sequence header (0x17)] + [AVC config packet (0x00)] + [composition time (0x000000)] + [AVCDecoderConfigurationRecord]
	if len(data) < 10 {
		return false
	}
	
	// 检查是否是AVC sequence header (0x17 0x00 0x00 0x00 0x01)
	return data[0] == 0x17 &&  // AVC video tag
		   data[1] == 0x00 &&  // AVC sequence header
		   data[2] == 0x00 && 
		   data[3] == 0x00 && 
		   data[4] == 0x01     // AVCPacketType
}

// isAudioConfig 检查数据是否为音频配置信息 (AudioSpecificConfig)
func isAudioConfig(data []byte) bool {
	// 检查是否为AAC sequence header
	// 格式：[AAC sequence header (0xAF)] + [AAC config packet (0x00)]
	if len(data) < 4 {
		return false
	}
	
	// 检查是否是AAC sequence header (0xAF 0x00)
	return data[0] == 0xAF &&  // AAC audio tag
		   data[1] == 0x00    // AAC sequence header
}