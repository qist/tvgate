package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/qist/tvgate/cache"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/lb"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/proxy"
	"github.com/qist/tvgate/rules"
	tsync "github.com/qist/tvgate/utils/sync"
)

// FetchViaProxyGroup 按域名规则选代理组、经代理节点发起上游 GET 请求，
// 与 /https:// 原生转发路径同一套机制：rules.ChooseProxyGroup → lb.SelectProxy →
// proxy.CreateProxyClient（含节点健康标记与重试）。
//
// preferred 非空时强制使用该代理组（跳过域名规则匹配）：播放列表经代理拉到后，
// 分片 CDN 常是规则覆盖不到的 IP/内网地址，需沿用同一代理出口。
//
// 重定向回写：跟随 301/302/307 时把「原域名 → 目标 IP/域名」写入 redirect 链缓存
// （cache.AddRedirectIP），后续 IP 形态的分片 host 可经 rules.GetRedirectChainHosts
// 匹配回同一代理组（域名跳 IP 场景）。
//
// 返回值约定：
//   - (resp, pg, nil)  代理拉流成功，pg = 实际使用的代理组
//   - (nil, nil, nil)  未命中代理组 / 屡次选不到可用节点 → 调用方应直连兜底
//   - (nil, pg, err)   代理请求失败（重试耗尽）；ctx.Canceled 原样返回
//
// header 为发起上游请求的请求头（如 UA），streaming=true 时不设整体超时（流媒体长连接）。
func FetchViaProxyGroup(ctx context.Context, targetURL string, header http.Header, streaming bool, preferred *config.ProxyGroupConfig) (*http.Response, *config.ProxyGroupConfig, error) {
	host := ""
	if parsed, perr := url.Parse(targetURL); perr == nil {
		host = parsed.Hostname()
	}

	pg := preferred
	if pg == nil && host != "" {
		pg = rules.ChooseProxyGroup(host, "")
	}
	if pg == nil {
		return nil, nil, nil
	}

	maxRetries := pg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 1
	}
	retryDelay := pg.RetryDelay

	for attempt := 0; attempt <= maxRetries; attempt++ {
		forceTest := attempt > 0

		// 异步选择代理节点（与原生 /https:// 路径一致，超时内选不到则放弃）
		proxyCtx, proxyCancel := context.WithTimeout(ctx, config.DefaultDialTimeout)
		proxyRes := make(chan *config.ProxyConfig, 1)
		var wg tsync.WaitGroup
		wg.Go(func() {
			proxyRes <- lb.SelectProxy(proxyCtx, pg, targetURL, forceTest)
		})

		var selectedProxy *config.ProxyConfig
		select {
		case selectedProxy = <-proxyRes:
			proxyCancel()
		case <-proxyCtx.Done():
			proxyCancel()
			selectedProxy = nil
		}

		if selectedProxy == nil {
			// 选不到节点：重试后转直连
			if attempt == maxRetries {
				logger.LogPrintf("[proxyfetch] ❌ 未找到可用代理，转直连: %s", host)
				return nil, nil, nil
			}
			time.Sleep(retryDelay)
			continue
		}

		proxyClient, err := proxy.CreateProxyClient(ctx, &config.Cfg, *selectedProxy, pg.IPv6)
		if err != nil {
			markProxyResult(pg, selectedProxy, false)
			continue
		}
		if streaming {
			proxyClient.Timeout = 0 // 流媒体不设整体超时（client 为每次新建实例，可直接改字段）
		}
		// 服务端跟随重定向（最多 10 次），避免 302 Location 回流到浏览器泄露源地址；
		// 同时把「原域名 → 跳转 IP/域名」写入 redirect 链缓存，供域名跳 IP 场景匹配代理组
		proxyClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			prev := via[len(via)-1].URL
			if req.URL != nil && prev != nil {
				if o, t := prev.Hostname(), req.URL.Hostname(); o != "" && t != "" {
					cache.AddRedirectIP(o, t)
				}
			}
			logger.LogPrintf("[proxyfetch] ↪️ 跟随重定向: %v -> %v", prev, req.URL)
			return nil
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
		if err != nil {
			markProxyResult(pg, selectedProxy, false)
			continue
		}
		if header != nil {
			req.Header = header.Clone()
		}

		resp, err := proxyClient.Do(req)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil, pg, ctx.Err()
			}
			logger.LogPrintf("[proxyfetch] ⚠️ 代理请求网络错误（第 %d 次）: %v", attempt+1, err)
			markProxyResult(pg, selectedProxy, false)
			if attempt == maxRetries {
				return nil, pg, err
			}
			time.Sleep(retryDelay)
			continue
		}

		if resp.StatusCode >= 500 {
			resp.Body.Close()
			logger.LogPrintf("[proxyfetch] ⚠️ 代理服务器错误状态码 %d（第 %d 次）", resp.StatusCode, attempt+1)
			markProxyResult(pg, selectedProxy, false)
			if attempt == maxRetries {
				return nil, pg, fmt.Errorf("代理服务器错误状态码: %d", resp.StatusCode)
			}
			time.Sleep(retryDelay)
			continue
		}

		// 成功：响应体读超时保护（与原生路径一致）
		resp.Body = NewTimeoutReadCloser(resp.Body, 10*time.Second)
		markProxyResult(pg, selectedProxy, true)
		logger.LogPrintf("[proxyfetch] ✅ 经代理 %s 拉流: %s", selectedProxy.Name, host)
		return resp, pg, nil
	}
	return nil, nil, nil
}
