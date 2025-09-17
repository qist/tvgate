package lb

import (
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
)

// 异步写入所有测速结果
func ConsumeRemainingResults(ch chan config.TestResult, count int, group *config.ProxyGroupConfig, now time.Time) {
	interval := group.Interval
	if interval == 0 {
		interval = 60 * time.Second
	}

	for i := 0; i < count; i++ {
		res := <-ch
		group.Stats.Lock()
		stats := group.Stats.ProxyStats[res.Proxy.Name]
		if stats == nil {
			stats = &config.ProxyStats{}
			group.Stats.ProxyStats[res.Proxy.Name] = stats
		}
		stats.LastCheck = now

		if res.Err == nil &&
			res.ResponseTime > 0 {
			stats.Alive = true
			stats.ResponseTime = res.ResponseTime
			stats.StatusCode = res.StatusCode
			stats.FailCount = 0
			stats.CooldownUntil = time.Time{}
			logger.LogPrintf("✅ 异步：代理 %s 测速成功: %v（已写入缓存）", res.Proxy.Name, res.ResponseTime)
		} else {
			stats.Alive = false
			stats.FailCount++
			var cooldown time.Duration
			if stats.FailCount >= 15 {
				cooldown = 2 * time.Hour
			} else if stats.FailCount >= 3 {
				cooldown = interval
			}
			stats.CooldownUntil = now.Add(cooldown)
			logger.LogPrintf("❌ 异步：代理 %s 连续失败 %d 次，进入冷却 %v", res.Proxy.Name, stats.FailCount, cooldown)
		}
		group.Stats.Unlock()
	}
}
