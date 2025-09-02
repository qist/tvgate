package clear

import (
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	"time"
)

// 定时清理超过 maxAge 时间未使用的重定向链
func StartRedirectChainCleaner(interval time.Duration, maxAge time.Duration, stopCh <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			now := time.Now()
			config.RedirectCache.Lock()
			for domain, chainData := range config.RedirectCache.Mapping {
				if now.Sub(chainData.LastUsed) > maxAge {
					delete(config.RedirectCache.Mapping, domain)
					logger.LogPrintf("定时清理重定向链缓存: %s", domain)
				}
			}
			config.RedirectCache.Unlock()
		case <-stopCh:
			logger.LogPrintf("重定向链缓存清理器已停止")
			return
		}
	}
}
