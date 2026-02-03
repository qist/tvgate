package stream

import (
	"context"
	"sync"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/qist/tvgate/utils/buffer/ringbuffer"
)

const (
	StateStopped = iota
	StatePlaying
	StateError
)

type StreamHubs struct {
	mu       sync.RWMutex // 使用读写锁提高并发性能
	clients  map[*ringbuffer.RingBuffer]struct{}
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
		clients: make(map[*ringbuffer.RingBuffer]struct{}),
		state:   StateStopped,
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
	hub.clients[ch] = struct{}{}
}

func (hub *StreamHubs) RemoveClient(ch *ringbuffer.RingBuffer) {
	hub.mu.Lock()
	defer hub.mu.Unlock()

	// 检查channel是否还在clients映射中
	if _, exists := hub.clients[ch]; exists {
		delete(hub.clients, ch)
		ch.Close()
	}
	// 如果channel不存在于clients映射中，说明已经被Broadcast方法移除并关闭了
}

func (hub *StreamHubs) Broadcast(data []byte) {
	hub.mu.RLock() // 使用读锁，提高并发性能
	clients := make([]*ringbuffer.RingBuffer, 0, len(hub.clients))
	for ch := range hub.clients {
		clients = append(clients, ch)
	}
	hub.mu.RUnlock()

	for _, ch := range clients {
		buf := make([]byte, len(data))
		copy(buf, data)
		if !ch.Push(buf) {
			// 如果推送失败，可能是通道已关闭，从客户端列表中移除
			hub.removeClientIfNotExist(ch)
		}
	}
}

// removeClientIfNotExist 从客户端列表中移除已不存在的客户端
func (hub *StreamHubs) removeClientIfNotExist(ch *ringbuffer.RingBuffer) {
	hub.mu.Lock()
	defer hub.mu.Unlock()

	// 再次确认客户端是否还在列表中
	if _, exists := hub.clients[ch]; exists {
		delete(hub.clients, ch)
		ch.Close()
	}
}

func (hub *StreamHubs) ClientCount() int {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
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

	// 关闭RTSP客户端
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
	if hub.state == StateError {
		return false
	}

	// 如果已经在播放，直接返回
	if hub.state == StatePlaying {
		return true
	}

	// 等待状态变化或context取消
	for hub.state == StateStopped && !hub.isClosed && ctx.Err() == nil {
		hub.stateCond.Wait() // 这会自动释放锁并在唤醒时重新获取锁
	}

	// 检查context是否已取消
	if ctx.Err() != nil {
		return false
	}

	// 检查最终状态
	if hub.state == StateError {
		return false
	}
	return !hub.isClosed && hub.state == StatePlaying
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
