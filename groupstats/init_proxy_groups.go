package groupstats

import (
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/config"
	"time"
)

// initProxyGroups 初始化所有代理组的统计信息
func InitProxyGroups() {
	for groupName, group := range config.Cfg.ProxyGroups {
		if group == nil {
			logger.LogPrintf("⚠️ 代理组 %s 为 nil，跳过初始化", groupName)
			continue
		}
		if group.Stats == nil {
			group.Stats = &config.GroupStats{
				ProxyStats: make(map[string]*config.ProxyStats),
			}
		}
		for i, proxy := range group.Proxies {
			if proxy == nil {
				logger.LogPrintf("⚠️ 代理组 %s 中第 %d 个代理为 nil，跳过", groupName, i)
				continue
			}
			if proxy.Name == "" {
				logger.LogPrintf("⚠️ 代理组 %s 中第 %d 个代理名称为空，跳过", groupName, i)
				continue
			}
			if _, exists := group.Stats.ProxyStats[proxy.Name]; !exists {
				group.Stats.ProxyStats[proxy.Name] = &config.ProxyStats{
					LastCheck: time.Now(),
					Alive:     true,
				}
			}
		}
		logger.LogPrintf("✅ 初始化代理组 %s，共 %d 个代理", groupName, len(group.Proxies))
	}
}
