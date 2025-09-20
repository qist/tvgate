package stream

import (
	"context"
	"sync"

	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/description"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
)

// Stream state constants
const (
	StateStopped = iota
	StatePlaying
	StateError
)

// StreamHubs manages client connections for a specific stream
type StreamHubs struct {
	mu       sync.Mutex
	clients  map[*StreamRingBuffer]struct{}
	isClosed bool

	state     int // stopped, playing, error
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
		clients: make(map[*StreamRingBuffer]struct{}),
		state:   StateStopped,
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
	clients := make([]*StreamRingBuffer, 0, len(hub.clients))
	for ch := range hub.clients {
		clients = append(clients, ch)
	}
	hub.mu.Unlock()

	// 使用对象池避免每次分配新 slice
	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)

	for _, ch := range clients {
		ch.Push(dataCopy)
	}
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

	if rtspClient != nil {
		rtspClient.Close()
	}
	for ch := range clients {
		ch.Close()
	}
}

// Set flow state
func (hub *StreamHubs) SetPlaying() {
	hub.mu.Lock()
	hub.state = StatePlaying
	hub.lastError = nil
	hub.stateCond.Broadcast()
	hub.mu.Unlock()
}

func (hub *StreamHubs) SetStopped() {
	hub.mu.Lock()
	hub.state = StateStopped
	hub.stateCond.Broadcast()
	hub.mu.Unlock()
}

func (hub *StreamHubs) SetError(err error) {
	hub.mu.Lock()
	hub.state = StateError
	hub.lastError = err
	hub.stateCond.Broadcast()
	hub.mu.Unlock()
}

func (hub *StreamHubs) GetLastError() error {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return hub.lastError
}

// WaitForPlaying waits until the stream is in playing state or context is canceled
func (hub *StreamHubs) WaitForPlaying(ctx context.Context) bool {
	hub.mu.Lock()
	defer hub.mu.Unlock()

	for {
		if hub.isClosed || hub.state == StateError {
			return false
		}
		if hub.state == StatePlaying {
			return true
		}

		waitCh := make(chan struct{})
		go func() {
			hub.mu.Lock()
			hub.stateCond.Wait()
			hub.mu.Unlock()
			close(waitCh)
		}()

		hub.mu.Unlock()
		select {
		case <-ctx.Done():
			hub.mu.Lock()
			return false
		case <-waitCh:
			hub.mu.Lock()
			// 状态变化后继续循环判断
		}
	}
}

// RTSP client management
func (hub *StreamHubs) SetRtspClient(client *gortsplib.Client) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.rtspClient = client
}

func (hub *StreamHubs) GetRtspClient() *gortsplib.Client {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return hub.rtspClient
}

func (hub *StreamHubs) HasRtspClient() bool {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return hub.rtspClient != nil
}

// Media info management
func (hub *StreamHubs) SetMediaInfo(media *description.Media, format interface{}) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.videoMedia = media
	hub.videoFormat = format
}

func (hub *StreamHubs) GetMediaInfo() (*description.Media, interface{}, *description.Media, *format.MPEG4Audio) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return hub.videoMedia, hub.videoFormat, hub.audioMedia, hub.audioFormat
}

func (hub *StreamHubs) SetAudioMediaInfo(media *description.Media, format *format.MPEG4Audio) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.audioMedia = media
	hub.audioFormat = format
}

// Type-safe accessors
func (hub *StreamHubs) GetVideoFormat() *format.MPEGTS {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if f, ok := hub.videoFormat.(*format.MPEGTS); ok {
		return f
	}
	return nil
}

func (hub *StreamHubs) GetAudioFormat() *format.MPEG4Audio {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return hub.audioFormat
}
