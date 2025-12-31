package stream

import (
	"sync"
	"time"

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
}

var GlobalChannelManager = &ChannelManager{
	channels:   make(map[string]*MulticastChannel),
	cacheSize:  16384,
	sessionTTL: 10 * time.Second,
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
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			cm.cleanup()
		}
	}()
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
