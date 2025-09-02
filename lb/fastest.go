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
// selectFastestProxy 使用最快的代理
func SelectFastestProxy(group *config.ProxyGroupConfig, targetURL string, forceTest bool) *config.ProxyConfig {
	ctx, cancel := context.WithTimeout(context.Background(), config.DefaultDialTimeout)
	defer cancel()
	now := time.Now()
	interval := group.Interval
	if interval == 0 {
		interval = 60 * time.Second
	}
	maxAcceptableRT := 3 * time.Second
	// minAcceptableRT := 100 * time.Microsecond

	group.Stats.Lock()
	n := len(group.Proxies)
	if n == 0 {
		group.Stats.Unlock()
		return nil
	}

	needCheck := forceTest // 如果强制测速，直接需要测速
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

	// ===== 缓存优先使用（非强制测速时）=====
	if !needCheck {
		logger.LogPrintf("🌀 当前代理组缓存状态：")
		var fastest *config.ProxyConfig
		minTime := time.Hour
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

		for i := range group.Proxies {
			proxy := group.Proxies[i]
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
			group.Stats.Unlock()
			logger.LogPrintf("⚡ 使用缓存数据选择最快代理: %s，响应时间: %v，上次测速已过: %v, 最小测速间隔: %v", fastest.Name, minTime,
				now.Sub(stats.LastCheck).Truncate(time.Second), interval)
			return fastest
		}
	}
	group.Stats.Unlock()

	// ===== 并发测速 =====
	resultChan := make(chan config.TestResult, n)

	tested := 0
	// 并发测速所有代理
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
				req.Header.Set("Range", "bytes=0-2047") // 测试前2048字节

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

loop:
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

			if res.Err == nil &&
				res.ResponseTime >= 0 && res.StatusCode < 500 {
				stats.Alive = true
				stats.ResponseTime = res.ResponseTime
				stats.FailCount = 0
				stats.CooldownUntil = time.Time{}
				group.Stats.Unlock()

				if !successReturned {
					logger.LogPrintf("🚀 立即返回测速成功代理: %s 响应时间: %v，状态码: %d", res.Proxy.Name, res.ResponseTime, res.StatusCode)
					successReturned = true
					// 异步消费剩余结果
					remaining := tested
					if successReturned {
						remaining -= 1
					}
					logger.LogPrintf("📥 异步处理剩余 %d 个测速结果", remaining)
					go ConsumeRemainingResults(resultChan, remaining, group, now)
					return &res.Proxy
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
	go ConsumeRemainingResults(resultChan, 0, group, now)
	return nil
}