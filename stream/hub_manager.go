package stream

import (
	"sync"
)

var (
	hubManager = make(map[string]*StreamHubs) // 全局管理多个 StreamHubs
	hubMu      sync.Mutex                     // 保护 hubManager 的互斥锁
)

// GetOrCreateHub 获取或创建一个 StreamHub
func GetOrCreateHubs(streamURL string) *StreamHubs {
	hubMu.Lock()
	defer hubMu.Unlock()

	// 如果 hub 已存在，检查是否可用
	if hub, exists := hubManager[streamURL]; exists {
		// 检查hub是否已经关闭
		hub.mu.Lock()
		isClosed := hub.isClosed
		hub.mu.Unlock()
		
		// 如果已关闭，移除旧的hub并创建新的
		if isClosed {
			delete(hubManager, streamURL)
		} else {
			return hub
		}
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