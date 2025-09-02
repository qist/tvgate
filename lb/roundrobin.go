package lb

import (
	"context"
	"fmt"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	p "github.com/qist/tvgate/proxy"
	"net/http"
	"strings"
	"time"
)

// selectRoundRobinProxy 使用轮询方式选择代理
func SelectRoundRobinProxy(group *config.ProxyGroupConfig, targetURL string, forceTest bool) *config.ProxyConfig {
	ctx, cancel := context.WithTimeout(context.Background(), config.DefaultDialTimeout)
	defer cancel()
	now := time.Now()

	interval := group.Interval
	if interval == 0 {
		interval = 60 * time.Second
	}

	threshold := group.MaxRT
	if threshold == 0 {
		threshold = 800 * time.Millisecond
	}

	minAcceptableRT := 100 * time.Microsecond

	group.Stats.Lock()
	n := len(group.Proxies)
	if n == 0 {
		group.Stats.Unlock()
		return nil
	}

	// ===== 是否需要测速 =====
	needCheck := forceTest
	if !forceTest {
		allNoRT := true

		for _, proxy := range group.Proxies {
			stats, ok := group.Stats.ProxyStats[proxy.Name]
			if ok && now.Sub(stats.LastCheck) <= interval && stats.ResponseTime > 0 {
				// 缓存有效且测速过，认为是“可用的”
				allNoRT = false
			}
		}

		// 至少有一个可用代理，就不测速
		// 全部未测速成功（或缓存过期），才触发测速
		needCheck = allNoRT
	}

	// 使用缓存选择代理
	if !needCheck {
		logger.LogPrintf("🌀 当前代理组缓存状态：")
		start := group.Stats.RoundRobinIndex
		var fallback *config.ProxyConfig

		for _, proxy := range group.Proxies {
			stats := group.Stats.ProxyStats[proxy.Name]
			if stats == nil {
				logger.LogPrintf(" - %-16s [未测速]", proxy.Name)
				continue
			}

			status := "❌死"
			if stats.Alive && now.After(stats.CooldownUntil) && stats.ResponseTime > 0 {
				status = "✅活"
			} else if stats.Alive && now.Before(stats.CooldownUntil) {
				status = "🚫冷"
			}

			cooldown := "无"
			if stats.CooldownUntil.After(now) {
				cooldown = fmt.Sprintf("冷却中(至 %s)", stats.CooldownUntil.Format("15:04:05"))
			}

			logger.LogPrintf(" - %-16s [%-3s] RT: %-10v 上次测速已过: %-6v 最小测速间隔: %-6v 失败次数: %-2d %s",
				proxy.Name,
				status,
				stats.ResponseTime.Truncate(time.Microsecond), // 保留更合理的精度
				now.Sub(stats.LastCheck).Truncate(time.Second),
				interval,
				stats.FailCount,
				cooldown,
			)
		}

		for i := 0; i < n; i++ {
			idx := (start + i) % n
			proxy := group.Proxies[idx]
			stats, ok := group.Stats.ProxyStats[proxy.Name]
			if !ok || !stats.Alive || now.Before(stats.CooldownUntil) {
				continue
			}

			if stats.ResponseTime >= minAcceptableRT && stats.ResponseTime <= threshold {
				group.Stats.RoundRobinIndex = (idx + 1) % n
				group.Stats.Unlock()
				logger.LogPrintf("🌀 使用缓存代理: %s 响应: %v", proxy.Name, stats.ResponseTime)
				return proxy
			}

			if fallback == nil && stats.ResponseTime > 0 {
				fallback = proxy
				group.Stats.RoundRobinIndex = (idx + 1) % n
			}
		}

		if fallback != nil {
			group.Stats.Unlock()
			logger.LogPrintf("🌀 没有快速代理，使用次优缓存代理: %s", fallback.Name)
			return fallback
		}

		logger.LogPrintf("🚫 没有触发测速条件，也无可用缓存代理，返回 nil")
		group.Stats.Unlock()
		return nil
	}
	group.Stats.Unlock()

	// 并发测速部分
	resultChan := make(chan config.TestResult, n)
	tested := 0

	for i := range group.Proxies {
		proxy := group.Proxies[i]

		group.Stats.Lock()
		stats := group.Stats.ProxyStats[proxy.Name]
		if stats != nil && now.Before(stats.CooldownUntil) {
			group.Stats.Unlock()
			continue
		}
		group.Stats.Unlock()

		tested++
		go func(proxy config.ProxyConfig) {
			if strings.HasPrefix(targetURL, "rtsp://") {
				// start := time.Now()
				rt, err := TestRTSPProxy(proxy, targetURL)
				resultChan <- config.TestResult{
					Proxy:        proxy,
					ResponseTime: rt,
					Err:          err,
					StatusCode:   200, // RTSP 没有 HTTP 状态码，这里用 200 表示成功
				}
			} else {
				proxyCtx, proxyCancel := context.WithTimeout(context.Background(), config.DefaultDialTimeout)
				defer proxyCancel()

				client, err := p.CreateProxyClient(proxyCtx, &config.Cfg, proxy, group.IPv6)
				if err != nil {
					resultChan <- config.TestResult{Proxy: proxy, Err: err}
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
					Proxy:        proxy,
					ResponseTime: duration,
					Err:          err,
					StatusCode:   statusCode,
				}
			}
		}(*proxy)
	}

	successReturned := false

LOOP:
	for i := 0; i < tested; i++ {
		select {
		case res := <-resultChan:
			group.Stats.Lock()
			stats := group.Stats.ProxyStats[res.Proxy.Name]
			if stats == nil {
				stats = &config.ProxyStats{}
				group.Stats.ProxyStats[res.Proxy.Name] = stats
			}
			stats.LastCheck = now

			if res.Err == nil && res.ResponseTime >= minAcceptableRT && res.StatusCode < 500 {
				stats.Alive = true
				stats.ResponseTime = res.ResponseTime
				stats.FailCount = 0
				stats.CooldownUntil = time.Time{}
				group.Stats.Unlock()

				logger.LogPrintf("🚀 测速成功: %s 响应时间: %v 状态码: %d", res.Proxy.Name, res.ResponseTime, res.StatusCode)

				if !successReturned {
					successReturned = true
					group.Stats.Lock()
					for idx := range group.Proxies {
						if group.Proxies[idx].Name == res.Proxy.Name {
							group.Stats.RoundRobinIndex = (idx + 1) % n
							break
						}
					}
					group.Stats.Unlock()

					if cached := SelectProxyFromCache(group, now); cached != nil {
						logger.LogPrintf("⚡ 使用缓存中最优代理: %s（由测速 %s 触发）", cached.Name, res.Proxy.Name)
						remaining := tested - 1
						logger.LogPrintf("📥 异步处理剩余 %d 个测速结果", remaining)
						go ConsumeRemainingResults(resultChan, remaining, group, now)
						return cached
					}
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
			logger.LogPrintf("⏰ 并发测速超时")
			break LOOP
		}
	}

	logger.LogPrintf("❌ 所有代理测速失败或无合适项")
	go ConsumeRemainingResults(resultChan, 0, group, now)
	return nil
}
