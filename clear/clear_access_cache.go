package clear

import (
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/utils/proxy"
)

// ClearAccessCacheByGroup 按组名清理访问缓存：删除所有指向该组的条目，返回删除数。
// 组名 = 组内首个代理的名称（GetGroupName 约定，与 StoreAccessCache 日志一致）。
// 相比按域名交集清理，按组名清理能覆盖所有配置变更场景：域名被删除、域名挪到
// 其它组、整组删除/改名——旧缓存条目里的组是旧配置代次的指针，域名交集比对会漏清
// （例如组内域名全部删光时交集为空），导致请求继续命中旧组、不走新规则。
func ClearAccessCacheByGroup(groupName string) int {
	if groupName == "" {
		return 0
	}

	config.AccessCache.Lock()
	defer config.AccessCache.Unlock()

	removed := 0
	for key, cached := range config.AccessCache.Mapping {
		if cached.Group != nil && proxy.GetGroupName(cached.Group) == groupName {
			delete(config.AccessCache.Mapping, key)
			removed++
			logger.LogPrintf("🗑️ 清理访问缓存条目: %s (组: %s)", key, groupName)
		}
	}
	return removed
}

// ResetProxyGroupStats 重置代理组测速统计：清空存活/响应时间/失败次数/冷却/测速时间，
// 下次请求时测速缓存判定（LastCheck 零值或超 interval）失效，强制重新测速。
func ResetProxyGroupStats(group *config.ProxyGroupConfig) {
	if group == nil || group.Stats == nil {
		return
	}
	group.Stats.Lock()
	defer group.Stats.Unlock()
	group.Stats.LastCheck = time.Time{}
	for _, stats := range group.Stats.ProxyStats {
		stats.LastCheck = time.Time{}
		stats.ResponseTime = 0
		stats.Alive = false
		stats.FailCount = 0
		stats.StatusCode = 0
		stats.CooldownUntil = time.Time{}
	}
}
