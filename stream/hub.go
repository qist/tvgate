package stream

import (
	"context"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/utils/buffer/ringbuffer"
	tsync "github.com/qist/tvgate/utils/sync"
)

const (
	StateStopped = iota
	StatePlaying
	StateError
)

type StreamHubs struct {
	mu        sync.RWMutex // 使用读写锁提高并发性能
	clients   map[*ringbuffer.RingBuffer]struct{}
	isClosed  bool
	key       string
	idleGen   uint64
	idleTimer *time.Timer
	// 添加生命周期管理
	ctx    context.Context
	cancel context.CancelFunc
	wg     tsync.WaitGroup
	// 添加流状态管理
	state     int // 0: stopped, 1: playing, 2: error
	stateCond *sync.Cond
	lastError error
	// 添加RTSP客户端引用
	rtspClient  *gortsplib.Client
	videoMedia  *description.Media
	videoFormat interface{}
	audioMedia  *description.Media
	audioFormat *format.MPEG4Audio
	setupMu     sync.Mutex // 用于同步初始化过程
}

func NewStreamHubs() *StreamHubs {
	ctx, cancel := context.WithCancel(config.ServerCtx)
	hub := &StreamHubs{
		clients: make(map[*ringbuffer.RingBuffer]struct{}),
		state:   StateStopped,
		ctx:     ctx,
		cancel:  cancel,
	}
	hub.stateCond = sync.NewCond(&hub.mu)
	return hub
}

func (hub *StreamHubs) AddClient(ch *ringbuffer.RingBuffer) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.isClosed {
		ch.Close()
		return
	}
	hub.cancelIdleCloseLocked()
	hub.clients[ch] = struct{}{}
}

func (hub *StreamHubs) RemoveClient(ch *ringbuffer.RingBuffer) {
	var rtspToClose *gortsplib.Client

	hub.mu.Lock()
	// 检查channel是否还在clients映射中
	if _, exists := hub.clients[ch]; exists {
		delete(hub.clients, ch)
		ch.Close()
	}
	if !hub.isClosed && len(hub.clients) == 0 {
		if hub.rtspClient != nil {
			rtspToClose = hub.rtspClient
			hub.rtspClient = nil
		}
		hub.state = StateStopped
		hub.scheduleIdleCloseLocked()
	}
	hub.mu.Unlock()

	if rtspToClose != nil {
		rtspToClose.Close()
	}
	// 如果channel不存在于clients映射中，说明已经被Broadcast方法移除并关闭了
}

func (hub *StreamHubs) Broadcast(data []byte) {
	hub.mu.RLock() // 使用读锁，提高并发性能
	clientCount := len(hub.clients)
	if clientCount == 0 {
		hub.mu.RUnlock()
		return
	}
	clients := make([]*ringbuffer.RingBuffer, 0, clientCount)
	for ch := range hub.clients {
		clients = append(clients, ch)
	}
	hub.mu.RUnlock()

	// 快速检测是否是 H264 关键帧（只检查前5字节）
	isKeyFrame := len(data) >= 5 &&
		data[0] == 0x00 && data[1] == 0x00 &&
		data[2] == 0x00 && data[3] == 0x01 &&
		(data[4]&0x1F) == 5 // NAL type 5 = IDR frame

	for _, ch := range clients {
		buf := make([]byte, len(data))
		copy(buf, data)
		if !ch.Push(buf) {
			if isKeyFrame {
				// 关键帧：等待一小段时间重试一次
				time.Sleep(30 * time.Millisecond)
				if !ch.Push(buf) {
					// 重试失败，记录日志但不移除客户端
					logger.LogPrintf("Key frame dropped for slow client")
				}
			}
			// 非关键帧：直接丢弃，不通知客户端，避免不必要的剔除
		}
	}
}

// removeClientIfNotExist 从客户端列表中移除已不存在的客户端
func (hub *StreamHubs) removeClientIfNotExist(ch *ringbuffer.RingBuffer) {
	var rtspToClose *gortsplib.Client

	hub.mu.Lock()
	// 再次确认客户端是否还在列表中
	if _, exists := hub.clients[ch]; exists {
		delete(hub.clients, ch)
		ch.Close()
	}
	if !hub.isClosed && len(hub.clients) == 0 {
		if hub.rtspClient != nil {
			rtspToClose = hub.rtspClient
			hub.rtspClient = nil
		}
		hub.state = StateStopped
		hub.scheduleIdleCloseLocked()
	}
	hub.mu.Unlock()

	if rtspToClose != nil {
		rtspToClose.Close()
	}
}

func (hub *StreamHubs) ClientCount() int {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	return len(hub.clients)
}

func (hub *StreamHubs) Close() {
	hub.mu.Lock()
	if hub.isClosed {
		hub.mu.Unlock()
		return
	}
	hub.isClosed = true
	hub.cancelIdleCloseLocked()
	hub.state = StateStopped
	hub.stateCond.Broadcast()

	// 清理媒体信息
	hub.videoMedia = nil
	hub.videoFormat = nil
	hub.audioMedia = nil
	hub.audioFormat = nil

	// 关闭RTSP客户端
	if hub.rtspClient != nil {
		hub.rtspClient.Close()
		hub.rtspClient = nil
	}

	for ch := range hub.clients {
		ch.Close()
	}
	hub.clients = nil

	// 取消上下文并等待 goroutine
	if hub.cancel != nil {
		hub.cancel()
	}
	hub.mu.Unlock()

	hub.wg.Wait()
}

func (hub *StreamHubs) scheduleIdleCloseLocked() {
	hub.idleGen++
	gen := hub.idleGen
	key := hub.key
	if key == "" {
		return
	}
	if hub.idleTimer != nil {
		hub.idleTimer.Stop()
	}
	hub.idleTimer = time.AfterFunc(10*time.Second, func() {
		hub.mu.RLock()
		closed := hub.isClosed
		empty := len(hub.clients) == 0
		sameGen := hub.idleGen == gen
		hub.mu.RUnlock()
		if closed || !empty || !sameGen {
			return
		}
		RemoveHub(key)
	})
}

func (hub *StreamHubs) cancelIdleCloseLocked() {
	hub.idleGen++
	if hub.idleTimer != nil {
		hub.idleTimer.Stop()
		hub.idleTimer = nil
	}
}

// GetContext 获取 hub 的上下文
func (hub *StreamHubs) GetContext() context.Context {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	return hub.ctx
}

// AddWG 为 hub 添加等待组计数
func (hub *StreamHubs) AddWG(n int) {
	hub.wg.Add(n)
}

// DoneWG 减少 hub 等待组计数
func (hub *StreamHubs) DoneWG() {
	hub.wg.Done()
}

// Go 启动一个协程并自动管理 WaitGroup 的计数
func (hub *StreamHubs) Go(f func()) {
	hub.wg.Go(f)
}

// 新增方法：设置流为播放状态
func (hub *StreamHubs) SetPlaying() {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.state = 1
	hub.lastError = nil
	hub.stateCond.Broadcast()
}

// 新增方法：设置流为停止状态
func (hub *StreamHubs) SetStopped() {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.state = 0
	hub.stateCond.Broadcast()
}

// SetError 设置流为错误状态
func (hub *StreamHubs) SetError(err error) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.state = StateError
	hub.lastError = err
	hub.stateCond.Broadcast()

	// 清除媒体信息
	hub.videoMedia = nil
	hub.videoFormat = nil
	hub.audioMedia = nil
	hub.audioFormat = nil

	// 如果有 RTSP 客户端，也将其关闭
	if hub.rtspClient != nil {
		hub.rtspClient.Close()
		hub.rtspClient = nil
	}

	// 关闭所有现有客户端通道，让他们重新连接
	for ch := range hub.clients {
		ch.Close()
	}
	hub.clients = make(map[*ringbuffer.RingBuffer]struct{})
}

// 新增方法：获取最后的错误
func (hub *StreamHubs) GetLastError() error {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return hub.lastError
}

// 新增方法：等待流变为播放状态
func (hub *StreamHubs) WaitForPlaying(ctx context.Context) bool {
	hub.mu.Lock()
	defer hub.mu.Unlock()

	// 如果已经关闭，直接返回
	if hub.isClosed {
		return false
	}

	// 如果在错误状态，返回错误
	if hub.state == StateError {
		return false
	}

	// 如果已经在播放，直接返回
	if hub.state == StatePlaying {
		return true
	}

	// 使用带超时的条件变量等待，支持 context 取消
	for {
		// 检查 context 是否已取消
		select {
		case <-ctx.Done():
			return false
		default:
		}

		// 检查状态
		if hub.isClosed || hub.state == StateError {
			return false
		}
		if hub.state == StatePlaying {
			return true
		}

		// 等待状态变化（最多等待 100ms，以便检查 context）
		hub.stateCond.Wait()
	}
}

// 新增方法：设置RTSP客户端
func (hub *StreamHubs) SetRtspClient(client *gortsplib.Client) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.rtspClient = client
}

// 新增方法：获取RTSP客户端
func (hub *StreamHubs) GetRtspClient() *gortsplib.Client {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	return hub.rtspClient
}

// 新增方法：检查RTSP客户端是否存在
func (hub *StreamHubs) HasRtspClient() bool {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	return hub.rtspClient != nil
}

// SetMediaInfo stores the video media and format for reuse
func (h *StreamHubs) SetMediaInfo(media *description.Media, format interface{}) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// 直接保存媒体信息，不进行类型转换
	h.videoMedia = media
	// 保存原始格式接口，后续通过类型断言使用
	h.videoFormat = format
}

// GetMediaInfo retrieves stored video media and format
func (h *StreamHubs) GetMediaInfo() (*description.Media, interface{}, *description.Media, *format.MPEG4Audio) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.videoMedia, h.videoFormat, h.audioMedia, h.audioFormat
}

// SetAudioMediaInfo stores the audio media and format for reuse
func (h *StreamHubs) SetAudioMediaInfo(media *description.Media, format *format.MPEG4Audio) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.audioMedia = media
	h.audioFormat = format
}

// ClearMediaInfo 清除媒体信息
func (h *StreamHubs) ClearMediaInfo() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.videoMedia = nil
	h.videoFormat = nil
	h.audioMedia = nil
	h.audioFormat = nil
}

// GetSetupLock 获取初始化锁
func (h *StreamHubs) GetSetupLock() {
	h.setupMu.Lock()
}

// ReleaseSetupLock 释放初始化锁
func (h *StreamHubs) ReleaseSetupLock() {
	h.setupMu.Unlock()
}
