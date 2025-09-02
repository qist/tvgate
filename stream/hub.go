package stream

import (
	"sync"
)

type StreamHubs struct {
	mu       sync.Mutex
	clients  map[chan []byte]struct{}
	isClosed bool
}

func NewStreamHubs() *StreamHubs {
	return &StreamHubs{
		clients: make(map[chan []byte]struct{}),
	}
}

func (hub *StreamHubs) AddClient(ch chan []byte) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.isClosed {
		close(ch)
		return
	}
	hub.clients[ch] = struct{}{}
}

func (hub *StreamHubs) RemoveClient(ch chan []byte) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	delete(hub.clients, ch)
	close(ch)
}

func (hub *StreamHubs) Broadcast(data []byte) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	for ch := range hub.clients {
		select {
		case ch <- data:
		default:
			// 如果客户端缓冲区满了，移除客户端
			delete(hub.clients, ch)
			close(ch)
		}
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
	for ch := range hub.clients {
		close(ch)
	}
	hub.clients = nil
}
