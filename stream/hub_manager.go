package stream

import (
	"sync"
)

var (
	hubManager = make(map[string]*StreamHubs) // 全局管理多个 StreamHubs
	hubMu      sync.RWMutex                   // 使用读写锁提高并发性能
)

// GetOrCreateHub 获取或创建一个 StreamHub
func GetOrCreateHubs(streamURL string) *StreamHubs {
	hubMu.RLock()
	// 先尝试读取已存在的hub
	if hub, exists := hubManager[streamURL]; exists {
		// 检查hub是否已经关闭
		hub.mu.Lock()
		isClosed := hub.isClosed
		hub.mu.Unlock()

		if !isClosed {
			hubMu.RUnlock()
			return hub
		}
		// 如果已关闭，释放读锁，获取写锁来移除旧的hub
		hubMu.RUnlock()
		hubMu.Lock()
		// 双重检查，确保没有其他goroutine已经创建了新的hub
		if hub, exists := hubManager[streamURL]; exists {
			hub.mu.Lock()
			stillClosed := hub.isClosed
			hub.mu.Unlock()
			if stillClosed {
				delete(hubManager, streamURL)
				hubMu.Unlock()
				hub.Close() // 在锁外关闭
				hubMu.Lock()
			}
		}
		hubMu.Unlock()
	} else {
		hubMu.RUnlock()
	}

	// 获取写锁来创建新的hub
	hubMu.Lock()
	defer hubMu.Unlock()

	// 再次检查是否已经有其他goroutine创建了hub
	if hub, exists := hubManager[streamURL]; exists {
		// 检查这个hub是否已经关闭
		hub.mu.Lock()
		isClosed := hub.isClosed
		hub.mu.Unlock()

		if !isClosed {
			return hub
		}
		// 如果已关闭，移除并创建新的
		delete(hubManager, streamURL)
		hubMu.Unlock()
		hub.Close() // 在锁外关闭
		hubMu.Lock()
	}

	// 创建一个新的 hub
	hub := NewStreamHubs()
	hubManager[streamURL] = hub
	return hub
}

// RemoveHubIfEmpty 只有在没有客户端时才移除 StreamHub
func RemoveHubIfEmpty(streamURL string, hub *StreamHubs) {
	hubMu.Lock()
	// 双重检查：确保我们移除的是同一个 hub 且它确实没有客户端了
	currentHub, exists := hubManager[streamURL]
	if !exists || currentHub != hub {
		hubMu.Unlock()
		return
	}

	if hub.ClientCount() > 0 {
		hubMu.Unlock()
		return
	}

	delete(hubManager, streamURL)
	hubMu.Unlock()

	hub.Close() // 在锁外关闭
}

// RemoveHub 强制移除一个 StreamHub
func RemoveHub(streamURL string) {
	hubMu.Lock()
	// 从 hubManager 中删除指定的 hub
	hub, exists := hubManager[streamURL]
	if exists {
		delete(hubManager, streamURL)
	}
	hubMu.Unlock()

	if exists {
		hub.Close() // 在锁外关闭，避免在 Wait 时持有全局锁导致死锁
	}
}
