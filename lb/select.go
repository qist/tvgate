package lb

import (
	"context"
	"strings"
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
)

// SelectProxy 根据策略选择代理
func SelectProxy(ctx context.Context, group *config.ProxyGroupConfig, targetURL string, forceTest bool) *config.ProxyConfig {
	if group == nil {
		return nil
	}

	if len(group.ActiveProxies()) == 0 {
		logger.LogPrintf("代理组中没有可用代理")
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
		markProxyUsed(group, proxy)
		return proxy
	default:
		proxy := SelectRoundRobinProxy(ctx, group, targetURL, forceTest)
		markProxyUsed(group, proxy)
		return proxy
	}
}

// markProxyUsed 记录节点最近一次被选路使用的时间。
// 池探测据此判断节点是否正在承载播放：正在播放的节点不做 2MB/RTP 吞吐采样，
// 避免采样抢带宽、以及被播放流量拉低后误判成「空闲端口更快」。
func markProxyUsed(group *config.ProxyGroupConfig, proxy *config.ProxyConfig) {
	if group == nil || proxy == nil || group.Stats == nil {
		return
	}
	group.Stats.Lock()
	st := group.Stats.ProxyStats[proxy.Name]
	if st == nil {
		st = &config.ProxyStats{}
		group.Stats.ProxyStats[proxy.Name] = st
	}
	st.LastUsed = time.Now()
	group.Stats.Unlock()
}
