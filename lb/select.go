package lb

import (
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	"strings"
)

// selectProxy 根据策略选择代理
func SelectProxy(group *config.ProxyGroupConfig, targetURL string, forceTest bool) *config.ProxyConfig {
	config.LogConfigMutex.Lock()
	defer config.LogConfigMutex.Unlock()

	if len(group.Proxies) == 0 {
		logger.LogPrintf("代理组中没有代理")
		return nil
	}
	logger.LogPrintf("代理组中有 %d 个代理", len(group.Proxies))

	if group.Stats == nil {
		group.Stats = &config.GroupStats{
			ProxyStats: make(map[string]*config.ProxyStats),
		}
	}

	switch strings.ToLower(group.LoadBalance) {
	case "fastest":
		proxy := SelectFastestProxy(group, targetURL, forceTest)
		if proxy != nil {
			logger.LogPrintf("选择最快的代理: %s", proxy.Name)
		}
		return proxy
	default:
		proxy := SelectRoundRobinProxy(group, targetURL, forceTest)
		if proxy != nil {
			logger.LogPrintf("轮询选择代理: %s", proxy.Name)
		}
		return proxy
	}
}
