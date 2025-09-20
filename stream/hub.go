package stream

import (
	"context"
	"sync"

	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/description"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
)

// StreamHubs manages client connections for a specific stream
type StreamHubs struct {
	mu       sync.Mutex
	clients  map[*StreamRingBuffer]struct{}
	isClosed bool
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
}

func NewStreamHubs() *StreamHubs {
	hub := &StreamHubs{
		clients: make(map[*StreamRingBuffer]struct{}),
		state:   0,
	}
	hub.stateCond = sync.NewCond(&hub.mu)
	return hub
}

func (hub *StreamHubs) AddClient(ch *StreamRingBuffer) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.isClosed {
		ch.Close()
		return
	}
	hub.clients[ch] = struct{}{}
}

func (hub *StreamHubs) RemoveClient(ch *StreamRingBuffer) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if _, exists := hub.clients[ch]; exists {
		delete(hub.clients, ch)
		ch.Close()
	}
}

func (hub *StreamHubs) Broadcast(data []byte) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	for ch := range hub.clients {
		ch.Push(data)
	}
}

func (hub *StreamHubs) ClientCount() int {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return len(hub.clients)
}

func (hub *StreamHubs) Close() {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.isClosed {
		return
	}
	hub.isClosed = true
	hub.state = 0
	hub.stateCond.Broadcast()
	if hub.rtspClient != nil {
		hub.rtspClient.Close()
		hub.rtspClient = nil
	}
	for ch := range hub.clients {
		ch.Close()
	}
	hub.clients = nil
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

// 新增方法：设置流为错误状态
func (hub *StreamHubs) SetError(err error) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.state = 2 // error state
	hub.lastError = err
	hub.stateCond.Broadcast()
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
	if hub.state == 2 {
		return false
	}

	// 如果已经在播放，直接返回
	if hub.state == 1 {
		return true
	}

	// 等待状态变为播放中或上下文取消
	for hub.state == 0 && !hub.isClosed {
		// 使用 Done channel 监听 context 取消
		done := make(chan struct{})
		go func() {
			defer close(done)
			hub.stateCond.Wait()
		}()

		select {
		case <-done:
			// 检查唤醒后的新状态
			if hub.state == 2 { // error state
				return false
			}
			if hub.state == 1 { // playing state
				return true
			}
		case <-ctx.Done():
			// context 被取消
			return false
		}
	}

	return !hub.isClosed && hub.state == 1
}

// 新增方法：设置RTSP客户端
func (hub *StreamHubs) SetRtspClient(client *gortsplib.Client) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.rtspClient = client
}

// 新增方法：获取RTSP客户端
func (hub *StreamHubs) GetRtspClient() *gortsplib.Client {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return hub.rtspClient
}

// 新增方法：检查RTSP客户端是否存在
func (hub *StreamHubs) HasRtspClient() bool {
	hub.mu.Lock()
	defer hub.mu.Unlock()
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
