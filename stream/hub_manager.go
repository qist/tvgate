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

	// 如果 hub 已存在，直接返回
	if hub, exists := hubManager[streamURL]; exists {
		return hub
	}

	// 否则创建一个新的 hub
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
