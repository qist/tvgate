package lb

import (
	"time"

	"github.com/qist/tvgate/config"
)

func SelectProxyFromCache(group *config.ProxyGroupConfig, now time.Time) *config.ProxyConfig {
	group.Stats.Lock()
	defer group.Stats.Unlock()

	n := len(group.Proxies)
	start := group.Stats.RoundRobinIndex
	for i := 0; i < n; i++ {
		idx := (start + i) % n
		proxy := group.Proxies[idx]
		stats, ok := group.Stats.ProxyStats[proxy.Name]
		if ok && stats.Alive &&
			now.After(stats.CooldownUntil) &&
			stats.ResponseTime > 0 {

			group.Stats.RoundRobinIndex = (idx + 1) % n
			return proxy
		}
	}
	return nil
}
