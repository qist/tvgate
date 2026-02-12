package lb

import (
	"context"
	"strings"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
)

// SelectProxy 根据策略选择代理
func SelectProxy(ctx context.Context, group *config.ProxyGroupConfig, targetURL string, forceTest bool) *config.ProxyConfig {
	if group == nil {
		return nil
	}

	if len(group.Proxies) == 0 {
		logger.LogPrintf("代理组中没有代理")
		return nil
	}
	// logger.LogPrintf("代理组中有 %d 个代理", len(group.Proxies))

	// 确保 Stats 已初始化 (双检锁模式)
	if group.Stats == nil {
		config.CfgMu.Lock()
		if group.Stats == nil {
			group.Stats = &config.GroupStats{
				ProxyStats: make(map[string]*config.ProxyStats),
			}
		}
		config.CfgMu.Unlock()
	}

	switch strings.ToLower(group.LoadBalance) {
	case "fastest":
		proxy := SelectFastestProxy(ctx, group, targetURL, forceTest)
		return proxy
	default:
		proxy := SelectRoundRobinProxy(ctx, group, targetURL, forceTest)
		return proxy
	}
}
