package lb

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	p "github.com/qist/tvgate/proxy"
	tsync "github.com/qist/tvgate/utils/sync"
)

var lbWg tsync.WaitGroup

// SelectFastestProxy 使用最快的代理
func SelectFastestProxy(ctx context.Context, group *config.ProxyGroupConfig, targetURL string, forceTest bool) *config.ProxyConfig {
	ctx, cancel := context.WithTimeout(ctx, config.DefaultDialTimeout)
	defer cancel()
	now := time.Now()
	interval := group.Interval
	if interval == 0 {
		interval = 60 * time.Second
	}
	maxAcceptableRT := 3 * time.Second

	group.Stats.RLock()
	n := len(group.Proxies)
	group.Stats.RUnlock()
	if n == 0 {
		return nil
	}

	needCheck := forceTest
	if forceTest {
		// 即使是强制测速，也检查一下是否刚刚才测过（1秒内）
		// 防止外部重试逻辑导致的瞬时死循环测速
		justChecked := true
		group.Stats.RLock()
		for _, proxy := range group.Proxies {
			stats, ok := group.Stats.ProxyStats[proxy.Name]
			if !ok || now.Sub(stats.LastCheck) > 1*time.Second {
				justChecked = false
				break
			}
		}
		group.Stats.RUnlock()
		if justChecked {
			logger.LogPrintf("⚠️ 1秒内已触发过测速，跳过本次强制测速")
			needCheck = false
		}
	}

	if !needCheck {
		allNoRT := true
		group.Stats.RLock()
		for _, proxy := range group.Proxies {
			stats, ok := group.Stats.ProxyStats[proxy.Name]
			if ok && now.Sub(stats.LastCheck) <= interval {
				// 只要有代理在最近测速过（无论成功失败），就不强制触发全局测速
				allNoRT = false
				break
			}
		}
		group.Stats.RUnlock()

		// 至少有一个可用代理，就不测速
		// 全部未测速成功（或缓存过期），才触发测速
		needCheck = allNoRT
	}

	// ===== 缓存优先使用（非强制测速时）=====
	if !needCheck {
		logger.LogPrintf("🌀 当前代理组缓存状态：")
		group.Stats.RLock()
		for _, proxy := range group.Proxies {
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

			logger.LogPrintf(" - %-16s [%-3s] RT: %-10v 上次测速已过: %-6v 最小测速间隔: %-6v HTTP状态: [%-3d] 失败次数: %-2d %s",
				proxy.Name,
				status,
				stats.ResponseTime.Truncate(time.Microsecond),
				now.Sub(stats.LastCheck).Truncate(time.Second),
				interval,
				stats.StatusCode,
				stats.FailCount,
				cooldown,
			)
		}

		var fastest *config.ProxyConfig
		minTime := time.Hour
		for _, proxy := range group.Proxies {
			stats, ok := group.Stats.ProxyStats[proxy.Name]
			if !ok {
				continue
			}
			if now.Before(stats.CooldownUntil) || !stats.Alive || stats.ResponseTime > maxAcceptableRT {
				continue
			}
			if stats.ResponseTime < minTime && stats.ResponseTime > 0 {
				minTime = stats.ResponseTime
				fastest = proxy
			}
		}

		if fastest != nil {
			stats := group.Stats.ProxyStats[fastest.Name]
			logger.LogPrintf("⚡ 使用缓存数据选择最快代理: %s，响应时间: %v，上次测速已过: %v, 最小测速间隔: %v", fastest.Name, minTime,
				now.Sub(stats.LastCheck).Truncate(time.Second), interval)
			group.Stats.RUnlock()
			return fastest
		}
		group.Stats.RUnlock()
	}

	// ===== 并发测速 =====
	logger.LogPrintf("🌐 启动并发测速 (原因: 缓存失效或 forceTest)")

	// 更新 LastCheck 避免并发触发
	group.Stats.Lock()
	for _, proxy := range group.Proxies {
		stats, ok := group.Stats.ProxyStats[proxy.Name]
		if !ok {
			stats = &config.ProxyStats{}
			group.Stats.ProxyStats[proxy.Name] = stats
		}
		stats.LastCheck = now
	}
	group.Stats.Unlock()

	resultChan := make(chan config.TestResult, n)
	var wg tsync.WaitGroup

	tested := 0
	for i := range group.Proxies {
		proxy := group.Proxies[i]

		group.Stats.RLock()
		stats := group.Stats.ProxyStats[proxy.Name]
		if stats != nil && now.Before(stats.CooldownUntil) {
			group.Stats.RUnlock()
			continue
		}
		group.Stats.RUnlock()

		tested++
		wg.Go(func() {
			pCopy := *proxy
			if strings.HasPrefix(targetURL, "rtsp://") {
				rt, err := TestRTSPProxy(ctx, pCopy, targetURL)
				resultChan <- config.TestResult{
					Proxy:        pCopy,
					ResponseTime: rt,
					Err:          err,
					StatusCode:   200,
				}
			} else {
				proxyCtx, proxyCancel := context.WithTimeout(ctx, config.DefaultDialTimeout)
				defer proxyCancel()

				client, err := p.CreateProxyClient(proxyCtx, &config.Cfg, pCopy, group.IPv6)
				if err != nil {
					resultChan <- config.TestResult{Proxy: pCopy, Err: err}
					return
				}

				req, _ := http.NewRequestWithContext(proxyCtx, "GET", targetURL, nil)
				req.Header.Set("Range", "bytes=0-2047")

				start := time.Now()
				resp, err := client.Do(req)
				duration := time.Since(start)
				if err == nil && resp != nil {
					resp.Body.Close()
				}

				statusCode := 0
				if resp != nil {
					statusCode = resp.StatusCode
				}

				resultChan <- config.TestResult{
					Proxy:        pCopy,
					ResponseTime: duration,
					Err:          err,
					StatusCode:   statusCode,
				}
			}
		})
	}

	successReturned := false
	consumed := 0
loop:
	for i := 0; i < tested; i++ {
		select {
		case res := <-resultChan:
			consumed++
			group.Stats.Lock()
			stats := group.Stats.ProxyStats[res.Proxy.Name]
			if stats == nil {
				stats = &config.ProxyStats{}
				group.Stats.ProxyStats[res.Proxy.Name] = stats
			}
			stats.LastCheck = now

			if res.Err == nil && res.ResponseTime >= 0 && res.StatusCode < 500 {
				stats.Alive = true
				stats.ResponseTime = res.ResponseTime
				stats.StatusCode = res.StatusCode
				stats.FailCount = 0
				stats.CooldownUntil = time.Time{}
				group.Stats.Unlock()

				if !successReturned {
					logger.LogPrintf("🚀 立即返回测速成功代理: %s 响应时间: %v，状态码: %d", res.Proxy.Name, res.ResponseTime, res.StatusCode)
					successReturned = true
					remaining := tested - consumed
					logger.LogPrintf("📥 异步处理剩余 %d 个测速结果", remaining)
					lbWg.Go(func() {
						ConsumeRemainingResults(resultChan, remaining, group, now)
					})

					// 返回原始代理对象的指针
					for _, p := range group.Proxies {
						if p.Name == res.Proxy.Name {
							return p
						}
					}
					return &res.Proxy // 兜底，虽然不应该走到这里
				}
			} else {
				if res.Err != nil {
					logger.LogPrintf("❌ 代理 %s 测速失败: %v", res.Proxy.Name, res.Err)
				} else {
					logger.LogPrintf("⚠️ 代理 %s 状态码异常: %d", res.Proxy.Name, res.StatusCode)
				}

				stats.Alive = false
				stats.ResponseTime = 0
				stats.FailCount++
				if stats.FailCount >= 3 {
					stats.CooldownUntil = now.Add(interval)
					logger.LogPrintf("❌ 代理 %s 连续失败 %d 次，进入冷却 %v", res.Proxy.Name, stats.FailCount, interval)
				}
				group.Stats.Unlock()
			}
		case <-ctx.Done():
			logger.LogPrintf("⏰ 测速超时，跳出循环")
			break loop
		}
	}

	logger.LogPrintf("❌ 无可用代理立即返回，全部失败或响应超时")
	remaining := tested - consumed
	lbWg.Go(func() {
		ConsumeRemainingResults(resultChan, remaining, group, now)
	})
	return nil
}
