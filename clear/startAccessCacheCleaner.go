package clear

import (
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	"time"
)

func StartAccessCacheCleaner(interval time.Duration, maxAge time.Duration, stopCh <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			now := time.Now()
			config.AccessCache.Lock()
			for key, cached := range config.AccessCache.Mapping {
				if now.Sub(cached.LastUsed) > maxAge {
					delete(config.AccessCache.Mapping, key)
					logger.LogPrintf("定时清理访问缓存: %s", key)
				}
			}
			config.AccessCache.Unlock()
		case <-stopCh:
			logger.LogPrintf("访问缓存清理器已停止")
			return
		}
	}
}
