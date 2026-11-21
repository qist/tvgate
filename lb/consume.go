package lb

import (
	"context"
	"errors"
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
		if res.Err != nil && errors.Is(res.Err, context.Canceled) {
			logger.LogPrintf("⏭️ 异步：代理 %s 测速被取消，跳过写入缓存", res.Proxy.Name)
			group.Stats.Unlock()
			continue
		}

		stats.LastCheck = now
		// 与主循环判定保持一致：有响应且状态码 <500 即可连（206 分片响应同样算成功）。
		if res.Err == nil && res.StatusCode < 500 {
			stats.Alive = true
			stats.ResponseTime = res.ResponseTime
			stats.StatusCode = res.StatusCode
			stats.FailCount = 0
			stats.CooldownUntil = time.Time{}
			logger.LogPrintf("✅ 异步：代理 %s 测速成功: %v（已写入缓存）", res.Proxy.Name, res.ResponseTime)
		} else {
			stats.Alive = false
			// 失败时清掉上一次的状态码/延迟，避免日志显示“206 + 失败次数 N”这种误导组合。
			stats.ResponseTime = 0
			stats.StatusCode = 0
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
