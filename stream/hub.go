package stream

import (
	"context"
	"sync"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	// "github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/utils/buffer/ringbuffer"
)

const (
	StateStopped = iota
	StatePlaying
	StateError
)

type StreamHubs struct {
	mu       sync.Mutex
	clients  map[*ringbuffer.RingBuffer]struct{}
	isClosed bool

	state     int
	stateCond *sync.Cond
	lastError error

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
	// logger.LogPrintf("DEBUG: New StreamHubs created")
	return hub
}

func (hub *StreamHubs) AddClient(ch *ringbuffer.RingBuffer) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.isClosed {
		ch.Close()
		// logger.LogPrintf("DEBUG: AddClient rejected, hub is closed")
		return
	}
	hub.clients[ch] = struct{}{}
	// logger.LogPrintf("DEBUG: Client added, total clients: %d", len(hub.clients))
}

func (hub *StreamHubs) RemoveClient(ch *ringbuffer.RingBuffer) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	
	// 检查客户端是否存在
	if _, exists := hub.clients[ch]; !exists {
		// logger.LogPrintf("DEBUG: Attempted to remove non-existent client")
		return
	}
	
	// 移除并关闭客户端
	delete(hub.clients, ch)
	ch.Close()
	// logger.LogPrintf("DEBUG: Client removed, total clients: %d", len(hub.clients))
	
	// 如果没有客户端了，关闭hub
	if len(hub.clients) == 0 && !hub.isClosed {
		// logger.LogPrintf("DEBUG: No clients left, closing hub")
		hub.isClosed = true
		hub.state = StateStopped
		hub.stateCond.Broadcast()
		
		// 关闭RTSP客户端
		if hub.rtspClient != nil {
			hub.rtspClient.Close()
			hub.rtspClient = nil
		} else {
			// logger.LogPrintf("DEBUG: No RTSP client to close")
		}
	}
}

func (hub *StreamHubs) Broadcast(data []byte) {
	hub.mu.Lock()
	clients := make([]*ringbuffer.RingBuffer, 0, len(hub.clients))
	for ch := range hub.clients {
		clients = append(clients, ch)
	}
	hub.mu.Unlock()

	for _, ch := range clients {
		buf := make([]byte, len(data))
		copy(buf, data)
		ch.Push(buf)
	}
	// logger.LogPrintf("DEBUG: Broadcasted %d bytes to %d clients", len(data), len(clients))
}

func (hub *StreamHubs) ClientCount() int {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return len(hub.clients)
}

func (hub *StreamHubs) Close() {
	hub.mu.Lock()
	if hub.isClosed {
		hub.mu.Unlock()
		return
	}
	hub.isClosed = true
	hub.state = StateStopped
	hub.stateCond.Broadcast()
	rtspClient := hub.rtspClient
	hub.rtspClient = nil
	clients := hub.clients
	hub.clients = nil
	hub.mu.Unlock()

	// logger.LogPrintf("DEBUG: Closing StreamHubs, closing %d clients", len(clients))
	if rtspClient != nil {
		rtspClient.Close()
	}
	for ch := range clients {
		ch.Close()
	}
}

func (hub *StreamHubs) SetPlaying() {
	hub.mu.Lock()
	hub.state = StatePlaying
	hub.lastError = nil
	hub.stateCond.Broadcast()
	hub.mu.Unlock()
	// logger.LogPrintf("DEBUG: Hub state set to PLAYING")
}

func (hub *StreamHubs) SetStopped() {
	hub.mu.Lock()
	hub.state = StateStopped
	hub.stateCond.Broadcast()
	hub.mu.Unlock()
	// logger.LogPrintf("DEBUG: Hub state set to STOPPED")
}

func (hub *StreamHubs) SetError(err error) {
	hub.mu.Lock()
	hub.state = StateError
	hub.lastError = err
	hub.stateCond.Broadcast()
	hub.mu.Unlock()
	// logger.LogPrintf("ERROR: Hub state set to ERROR: %v", err)
}

func (hub *StreamHubs) GetLastError() error {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return hub.lastError
}

// WaitForPlaying 等待流变为播放状态或上下文取消
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
	
	// 等待状态变为播放中或上下文取消
	for hub.state == StateStopped && !hub.isClosed {
		// 使用 Done channel 监听 context 取消
		done := make(chan struct{})
		go func() {
			defer close(done)
			hub.stateCond.Wait()
		}()
		
		select {
		case <-done:
			// 检查唤醒后的新状态
			if hub.state == StateError { // error state
				return false
			}
			if hub.state == StatePlaying { // playing state
				return true
			}
		case <-ctx.Done():
			// context 被取消
			return false
		}
	}
	
	return !hub.isClosed && hub.state == StatePlaying
}

func (hub *StreamHubs) HasRtspClient() bool {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return hub.rtspClient != nil && !hub.isClosed
}

func (hub *StreamHubs) GetRtspClient() *gortsplib.Client {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return hub.rtspClient
}

func (hub *StreamHubs) SetRtspClient(client *gortsplib.Client) {
	hub.mu.Lock()
	hub.rtspClient = client
	hub.mu.Unlock()
	// logger.LogPrintf("DEBUG: RTSP client set")
}

func (hub *StreamHubs) SetMediaInfo(media *description.Media, format interface{}) {
	hub.mu.Lock()
	hub.videoMedia = media
	hub.videoFormat = format
	hub.mu.Unlock()
	// logger.LogPrintf("DEBUG: Video media info set: media=%v, format=%T", media, format)
}

func (hub *StreamHubs) SetAudioMediaInfo(media *description.Media, format *format.MPEG4Audio) {
	hub.mu.Lock()
	hub.audioMedia = media
	hub.audioFormat = format
	hub.mu.Unlock()
	// logger.LogPrintf("DEBUG: Audio media info set: media=%v, format=%T", media, format)
}

func (hub *StreamHubs) GetMediaInfo() (*description.Media, interface{}, *description.Media, *format.MPEG4Audio) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return hub.videoMedia, hub.videoFormat, hub.audioMedia, hub.audioFormat
}
