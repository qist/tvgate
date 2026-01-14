package stream

import (
	"sync"
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
)

/*
===========================
Channel Manager
===========================
*/

type ChannelManager struct {
	mu        sync.RWMutex
	channels  map[string]*MulticastChannel
	cacheSize int

	sessionTTL time.Duration

	cleanerOnce sync.Once
}

// NewChannelManager 创建新的频道管理器
func NewChannelManager() *ChannelManager {
	// 从配置中获取FCC缓存大小
	config.CfgMu.RLock()
	fccCacheSize := config.Cfg.Server.FccCacheSize
	config.CfgMu.RUnlock()

	// 如果配置中没有设置或设置为0，默认使用16384
	if fccCacheSize <= 0 {
		fccCacheSize = 16384
	}

	return &ChannelManager{
		channels:   make(map[string]*MulticastChannel),
		cacheSize:  fccCacheSize,
		sessionTTL: 10 * time.Second,
	}
}

var GlobalChannelManager = NewChannelManager()

func (cm *ChannelManager) Get(channel string) *MulticastChannel {
	cm.mu.RLock()
	ch := cm.channels[channel]
	cm.mu.RUnlock()
	return ch
}

func (cm *ChannelManager) GetOrCreate(channel string) *MulticastChannel {
	cm.mu.RLock()
	ch := cm.channels[channel]
	cm.mu.RUnlock()

	if ch != nil {
		return ch
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()

	if ch = cm.channels[channel]; ch == nil {
		ch = NewMulticastChannel(channel, cm.cacheSize)
		cm.channels[channel] = ch
		logger.LogPrintf("[FCC] 创建频道 channel=%s", channel)

	}
	return ch
}

func (cm *ChannelManager) StartCleaner() {
	cm.cleanerOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()

			for range ticker.C {
				cm.cleanup()
			}
		}()
	})
}

func (cm *ChannelManager) cleanup() {
	now := time.Now()

	cm.mu.Lock()
	defer cm.mu.Unlock()

	for chID, ch := range cm.channels {
		ch.mu.Lock()
		for id, sess := range ch.Sessions {
			if now.Sub(sess.LastActive) > cm.sessionTTL {
				delete(ch.Sessions, id)
				logger.LogPrintf("[FCC] 会话超时 conn=%s channel=%s", id, chID)

			}
		}
		ch.mu.Unlock()

		if ch.RefCount() <= 0 {
			delete(cm.channels, chID)
			logger.LogPrintf("[FCC] 移除频道 channel=%s", chID)

		}
	}
}

// UpdateHubConfig 动态更新指定 StreamHub 的配置
func UpdateHubConfig(streamURL string, newConfig interface{}) error {
	hubMu.RLock()
	defer hubMu.RUnlock()

	hub, exists := hubManager[streamURL]
	if !exists {
		return nil // 如果hub不存在，返回nil，不视为错误
	}

	// 检查hub是否已经关闭
	hub.mu.Lock()
	isClosed := hub.isClosed
	hub.mu.Unlock()

	if isClosed {
		return nil // 如果hub已关闭，返回nil
	}

	// 在这里可以实现具体的配置更新逻辑
	// 目前只是占位符，后续可以扩展
	return nil
}

// UpdateAllHubsConfig 动态更新所有 StreamHubs 的配置
func UpdateAllHubsConfig(newConfig interface{}) {
	hubMu.RLock()
	defer hubMu.RUnlock()

	for streamURL, hub := range hubManager {
		// 检查hub是否已经关闭
		hub.mu.Lock()
		isClosed := hub.isClosed
		hub.mu.Unlock()

		if isClosed {
			continue // 如果hub已关闭，跳过
		}

		// 在这里可以实现具体的配置更新逻辑
		// 目前只是占位符，后续可以扩展
		_ = UpdateHubConfig(streamURL, newConfig)
	}
}
