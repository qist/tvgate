package groupstats

import (
	"github.com/qist/tvgate/clear"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	"reflect"
)

// 合并旧的 ProxyStats 到新配置，保持运行时状态
func MergeProxyStats(oldGroups, newGroups map[string]*config.ProxyGroupConfig) {
	for groupName, newGroup := range newGroups {
		oldGroup, ok := oldGroups[groupName]
		if !ok || oldGroup.Stats == nil {
			continue
		}

		// 初始化新组运行状态
		if newGroup.Stats == nil {
			newGroup.Stats = &config.GroupStats{
				LastCheck:       oldGroup.Stats.LastCheck,
				RoundRobinIndex: oldGroup.Stats.RoundRobinIndex,
				CurrentIndex:    oldGroup.Stats.CurrentIndex,
				ProxyStats:      make(map[string]*config.ProxyStats),
			}
		}

		// === 判断是否需要清理访问缓存 ===
		shouldClearCache := false

		// 判断代理节点是否变化（新增或删除）
		oldProxySet := make(map[string]bool)
		for _, proxy := range oldGroup.Proxies {
			oldProxySet[proxy.Name] = true
		}
		newProxySet := make(map[string]bool)
		for _, proxy := range newGroup.Proxies {
			newProxySet[proxy.Name] = true
		}
		for name := range oldProxySet {
			if !newProxySet[name] {
				shouldClearCache = true
				break
			}
		}
		for name := range newProxySet {
			if !oldProxySet[name] {
				shouldClearCache = true
				break
			}
		}

		// 判断代理组参数变化（域名、负载策略、测速参数等）
		if !shouldClearCache && isGroupConfigChanged(oldGroup, newGroup) {
			shouldClearCache = true
		}

		// 同步旧的代理状态
		for _, proxy := range newGroup.Proxies {
			if oldStat, exists := oldGroup.Stats.ProxyStats[proxy.Name]; exists {
				newGroup.Stats.ProxyStats[proxy.Name] = oldStat
			} else {
				newGroup.Stats.ProxyStats[proxy.Name] = &config.ProxyStats{}
			}
		}

		// 移除被删除的旧代理状态（仅日志提示，不复制）
		for oldName := range oldGroup.Stats.ProxyStats {
			if !newProxySet[oldName] {
				logger.LogPrintf("⚠️ 代理组 %s: 移除已删除代理 %s 的运行状态", groupName, oldName)
			}
		}

		// 清理访问缓存（按组名全量清理：覆盖域名删除/挪组等所有变更场景）
		if shouldClearCache {
			logger.LogPrintf("🧹 代理组 %s 配置变更，清理访问缓存", groupName)
			clear.ClearAccessCacheByGroup(groupName)
		}
	}

	// 整组删除/改名：旧组名下残留的缓存条目一并清理（新配置里已无此组，
	// 不清理的话请求会继续命中旧组指针，走已失效的代理规则）
	for oldName := range oldGroups {
		if _, ok := newGroups[oldName]; !ok {
			logger.LogPrintf("🧹 代理组 %s 已删除/改名，清理其访问缓存", oldName)
			clear.ClearAccessCacheByGroup(oldName)
		}
	}
}

// isGroupConfigChanged 判断代理组配置是否有变化
func isGroupConfigChanged(oldGroup, newGroup *config.ProxyGroupConfig) bool {
	normalizeGroup(oldGroup)
	normalizeGroup(newGroup)

	return !equalStringSlices(oldGroup.Domains, newGroup.Domains) ||
		oldGroup.Interval != newGroup.Interval ||
		oldGroup.LoadBalance != newGroup.LoadBalance ||
		oldGroup.MaxRetries != newGroup.MaxRetries ||
		oldGroup.RetryDelay != newGroup.RetryDelay ||
		oldGroup.MaxRT != newGroup.MaxRT ||
		oldGroup.IPv6 != newGroup.IPv6 ||
		!proxyListEqual(oldGroup.Proxies, newGroup.Proxies)
}

func normalizeGroup(g *config.ProxyGroupConfig) {
	if g.Domains == nil {
		g.Domains = []string{}
	}
	if g.Proxies == nil {
		g.Proxies = []*config.ProxyConfig{}
	}
	for _, p := range g.Proxies {
		if p.Headers == nil {
			p.Headers = map[string]string{}
		}
	}
}

func proxyListEqual(a, b []*config.ProxyConfig) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !proxyEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

func proxyEqual(a, b *config.ProxyConfig) bool {
	if a == nil || b == nil {
		return a == b // both must be nil
	}
	return a.Type == b.Type &&
		a.Server == b.Server &&
		a.Port == b.Port &&
		a.UDP == b.UDP &&
		a.Username == b.Username &&
		a.Password == b.Password &&
		a.Name == b.Name &&
		reflect.DeepEqual(a.Headers, b.Headers)
}

// equalStringSlices 比较两个字符串切片是否相等
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
