package clear

import (
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
)

func ClearAccessCache(domains []string) {
	if len(domains) == 0 {
		logger.LogPrintf("⚠️ clearAccessCache: 域名列表为空，跳过清理")
		return
	}

	domainSet := make(map[string]bool, len(domains))
	for _, d := range domains {
		domainSet[d] = true
	}

	config.AccessCache.Lock()
	defer config.AccessCache.Unlock()

	for key, cached := range config.AccessCache.Mapping {
		// 判断 cached.group.Domains 是否与 domains 有交集
		intersect := false
		for _, d := range cached.Group.Domains {
			if domainSet[d] {
				intersect = true
				break
			}
		}
		if intersect {
			delete(config.AccessCache.Mapping, key)
			logger.LogPrintf("🗑️ 清理访问缓存条目: %s", key)
		}
	}
}
