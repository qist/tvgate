package clear

import (
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	"time"
)

// StartGlobalProxyStatsCleaner 启动全局代理组定时清理任务
func StartGlobalProxyStatsCleaner(interval time.Duration, maxIdle time.Duration, stopCh <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			config.CfgMu.RLock()
			for _, group := range config.Cfg.ProxyGroups {
				cleanupProxyStats(group, maxIdle)
			}
			config.CfgMu.RUnlock()
		case <-stopCh:
			// logger.LogPrintf("全局代理组清理任务已停止")
			return
		}
	}
}

// cleanupProxyStats 清理单个代理组超过 maxIdle 的 ProxyStats 数据
func cleanupProxyStats(group *config.ProxyGroupConfig, maxIdle time.Duration) {
	now := time.Now()
	for _, proxy := range group.Proxies {
		stats, ok := group.Stats.ProxyStats[proxy.Name]
		if !ok {
			continue
		}

		// 如果上次测速时间超过 maxIdle
		if !stats.LastCheck.IsZero() && now.Sub(stats.LastCheck) > maxIdle {
			stats.ResponseTime = 0
			stats.Alive = false
			stats.FailCount = 0
			stats.StatusCode = 0 // 状态码重置
			stats.CooldownUntil = time.Time{}
			logger.LogPrintf("代理 [%s] 超过 %v 没活动的测速数据已清理", proxy.Name, maxIdle)
		}
	}
}
