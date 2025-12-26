package stream

import (
	"sync"
)

var (
	hubManager = make(map[string]*StreamHubs) // 全局管理多个 StreamHubs
	hubMu      sync.RWMutex                  // 使用读写锁提高并发性能
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
			hub.Close() // 确保hub被正确关闭
			delete(hubManager, streamURL)
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
		hub.Close()
		delete(hubManager, streamURL)
	}

	// 创建一个新的 hub
	hub := NewStreamHubs()
	hubManager[streamURL] = hub
	return hub
}

// RemoveHub 移除一个 StreamHub
func RemoveHub(streamURL string) {
	hubMu.Lock()
	defer hubMu.Unlock()

	// 从 hubManager 中删除指定的 hub
	if hub, exists := hubManager[streamURL]; exists {
		hub.Close() // 确保 hub 被正确关闭
		delete(hubManager, streamURL)
	}
}