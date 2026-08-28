package stream

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	tsync "github.com/qist/tvgate/utils/sync"
)

/*
===========================
Channel Manager
===========================
*/

type ChannelManager struct {
	channels  sync.Map // map[string]*MulticastChannel，读路径无锁，避免 FCC 热路径每包抢全局 RWMutex
	cacheSize int

	sessionTTL time.Duration

	stopCleaner chan struct{}
	cleanerOnce sync.Once
	Wg          tsync.WaitGroup
}

// NewChannelManager 创建新的频道管理器
func NewChannelManager() *ChannelManager {
	return &ChannelManager{
		channels:    sync.Map{},
		cacheSize:   0, // 延迟从配置读取
		sessionTTL:  10 * time.Second,
		stopCleaner: make(chan struct{}),
	}
}

// getCacheSize 延迟从配置读取FCC缓存大小，避免包初始化时配置未加载
func (cm *ChannelManager) getCacheSize() int {
	if cm.cacheSize > 0 {
		return cm.cacheSize
	}
	config.CfgMu.RLock()
	fccCacheSize := config.Cfg.Multicast.FccCacheSize
	config.CfgMu.RUnlock()
	if fccCacheSize <= 0 {
		fccCacheSize = 16384
	}
	cm.cacheSize = fccCacheSize
	return fccCacheSize
}

var GlobalChannelManager = NewChannelManager()

func (cm *ChannelManager) Get(channel string) *MulticastChannel {
	v, ok := cm.channels.Load(channel)
	if !ok {
		return nil
	}
	return v.(*MulticastChannel)
}

func (cm *ChannelManager) GetOrCreate(channel string) *MulticastChannel {
	if v, ok := cm.channels.Load(channel); ok {
		return v.(*MulticastChannel)
	}
	actual, _ := cm.channels.LoadOrStore(channel, NewMulticastChannel(channel, cm.getCacheSize()))
	if v, ok := actual.(*MulticastChannel); ok {
		return v
	}
	return nil
}

func (cm *ChannelManager) StartCleaner() {
	cm.cleanerOnce.Do(func() {
		cm.Wg.Go(func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()

			for {
				select {
				case <-ticker.C:
					cm.cleanup()
				case <-cm.stopCleaner:
					return
				}
			}
		})
	})
}

// Stop 停止清理器
func (cm *ChannelManager) Stop() {
	select {
	case <-cm.stopCleaner:
		// 已经关闭，直接等待
	default:
		close(cm.stopCleaner)
	}
	cm.Wg.Wait()
}

func (cm *ChannelManager) cleanup() {
	now := time.Now()

	var toDelete []string
	cm.channels.Range(func(key, value any) bool {
		chID := key.(string)
		ch := value.(*MulticastChannel)

		ch.mu.Lock()
		for id, sess := range ch.Sessions {
			if now.Sub(sess.LastActive) > cm.sessionTTL {
				delete(ch.Sessions, id)
				atomic.AddInt32(&ch.refCount, -1)
				logger.LogPrintf("[FCC] 会话超时 conn=%s channel=%s", id, chID)
			}
		}
		ch.mu.Unlock()

		if ch.RefCount() <= 0 {
			if ch.Cache != nil {
				ch.Cache.Reset()
			}
			toDelete = append(toDelete, chID)
			logger.LogPrintf("[FCC] 移除频道 channel=%s", chID)
		}
		return true
	})

	for _, chID := range toDelete {
		cm.channels.Delete(chID)
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
