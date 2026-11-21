package lb

import (
	"fmt"
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
)

func SelectProxyFromCache(group *config.ProxyGroupConfig, now time.Time) *config.ProxyConfig {
	group.Stats.Lock()
	defer group.Stats.Unlock()

	proxies := group.ActiveProxies()
	n := len(proxies)
	start := group.Stats.RoundRobinIndex

	// 有吞吐样本时选带宽最大的，避免测速后仍然随机落回低带宽节点。
	var best *config.ProxyConfig
	var bestThroughput int64
	bestIdx := -1
	for i := 0; i < n; i++ {
		idx := (start + i) % n
		proxy := proxies[idx]
		stats, ok := group.Stats.ProxyStats[proxy.Name]
		if !ok || !stats.Alive || !now.After(stats.CooldownUntil) {
			continue
		}
		if stats.Throughput > bestThroughput {
			best = proxy
			bestThroughput = stats.Throughput
			bestIdx = idx
		}
	}
	if best != nil {
		group.Stats.RoundRobinIndex = (bestIdx + 1) % n
		return best
	}

	for i := 0; i < n; i++ {
		idx := (start + i) % n
		proxy := proxies[idx]
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

// logProxyGroupStatus 打印一次组内节点缓存状态。
// 只在确定要测速时调用：播放器每秒刷新 m3u8 会反复走选路，
// 若每次命中缓存都打印，日志会被“当前代理组缓存状态”刷屏。
func logProxyGroupStatus(group *config.ProxyGroupConfig, proxies []*config.ProxyConfig, now time.Time, interval time.Duration) {
	if group == nil || group.Stats == nil {
		return
	}
	logger.LogPrintf("🌀 当前代理组缓存状态：")
	group.Stats.RLock()
	defer group.Stats.RUnlock()

	for _, proxy := range proxies {
		stats := group.Stats.ProxyStats[proxy.Name]
		if stats == nil {
			continue
		}

		status := "❌失"
		if stats.Alive && now.After(stats.CooldownUntil) && stats.ResponseTime > 0 {
			status = "✅活"
		} else if stats.Alive && now.Before(stats.CooldownUntil) {
			status = "🚫冷"
		}

		cooldown := "无"
		if stats.CooldownUntil.After(now) {
			cooldown = fmt.Sprintf("冷却中(至 %s)", stats.CooldownUntil.Format("15:04:05"))
		}

		logger.LogPrintf(" - %-16s [%-3s] RT: %-10v 吞吐: %-9s 上次测速已过: %-6v 最小测速间隔: %-6v HTTP状态: [%-3d] 失败次数: %-2d %s",
			proxy.Name,
			status,
			stats.ResponseTime.Truncate(time.Microsecond),
			formatThroughput(stats.Throughput),
			now.Sub(stats.LastCheck).Truncate(time.Second),
			interval,
			stats.StatusCode,
			stats.FailCount,
			cooldown,
		)
	}
}
