package http

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"

	"github.com/qist/tvgate/cache"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/dns"
	"github.com/qist/tvgate/logger"
)

const (
	maxRedirects = 10
)

// newTransport 构建自定义 DNS 拨号的 HTTP transport（项目 DNS 解析 + 多地址逐一拨号）。
func newTransport(c *config.Config) *http.Transport {
	// 获取自定义 Resolver 实例
	resolver := dns.GetInstance()

	// 基础 dialer
	baseDialer := &net.Dialer{
		Timeout:   c.HTTP.ConnectTimeout,
		KeepAlive: c.HTTP.KeepAlive,
	}

	// ✅ 自定义 DialContext，强制走 dns.GetInstance()
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}

			ips, err := resolver.LookupIPAddr(ctx, host)
			if err != nil || len(ips) == 0 {
				// logger.LogPrintf("⚠️ 自定义DNS解析失败 %s: %v, 尝试系统解析", host, err)
				return baseDialer.DialContext(ctx, network, addr) // fallback
			}

			// 尊重 DNS 返回顺序（内部 DNS 已做"谁快返回谁"的优选），
			// 按顺序逐个拨号，首个成功即返回；避免只取 ips[0] 撞上不可用地址后直接失败。
			var lastErr error
			for _, ia := range ips {
				if network == "tcp4" && ia.IP.To4() == nil {
					continue
				}
				if network == "tcp6" && ia.IP.To4() != nil {
					continue
				}
				conn, err := baseDialer.DialContext(ctx, network, net.JoinHostPort(ia.IP.String(), port))
				if err == nil {
					return conn, nil
				}
				lastErr = err
				if ctx.Err() != nil {
					return nil, err
				}
			}
			if lastErr != nil {
				return nil, lastErr
			}
			return baseDialer.DialContext(ctx, network, addr)
		},

		ResponseHeaderTimeout: c.HTTP.ResponseHeaderTimeout,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: *c.HTTP.InsecureSkipVerify},
		IdleConnTimeout:       c.HTTP.IdleConnTimeout,
		TLSHandshakeTimeout:   c.HTTP.TLSHandshakeTimeout,
		ExpectContinueTimeout: c.HTTP.ExpectContinueTimeout,
		MaxIdleConns:          c.HTTP.MaxIdleConns,
		MaxIdleConnsPerHost:   c.HTTP.MaxIdleConnsPerHost,
		MaxConnsPerHost:       c.HTTP.MaxConnsPerHost,
		DisableKeepAlives:     *c.HTTP.DisableKeepAlives,
	}
}

func NewHTTPClient(c *config.Config, transport *http.Transport) *http.Client {
	if transport == nil {
		transport = newTransport(c)
	}

	return &http.Client{
		Timeout:   c.HTTP.Timeout,
		Transport: transport,
		// 注意：返回 ErrUseLastResponse —— 本 client 不自动跟随重定向，
		// 3xx 交由调用方处理（如 stream.handleRedirect 回写同源地址）。
		// 这里仅记录「原域名 -> 跳转目标」到 redirect 链缓存，供域名跳 IP 场景匹配代理组。
		// 此前 hook 中的 req.URL = targetURL 重写是无效写（ErrUseLastResponse 分支会丢弃该请求）。
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			redirectCount := len(via)
			if redirectCount >= maxRedirects {
				return fmt.Errorf("超出最大重定向次数 (%d 次)", maxRedirects)
			}

			previousURL := via[redirectCount-1].URL
			redirectURLStr := req.Response.Header.Get("Location")
			redirectURL, err := url.Parse(redirectURLStr)
			if err != nil {
				return fmt.Errorf("无效的重定向 URL: %w", err)
			}

			targetURL := previousURL.ResolveReference(redirectURL)

			// 记录重定向
			if transport != nil {
				origin := previousURL.Hostname()
				target := targetURL.Hostname()
				if origin != "" && target != "" {
					cache.AddRedirectIP(origin, target)
				}
			}
			logger.LogPrintf("↪️ 从 %s 重定向到 %s", previousURL, targetURL)

			return http.ErrUseLastResponse
		},
	}
}

// NewPHPHTTPClient 供 PHP 模块使用的独立 HTTP client（与代理 NewHTTPClient 分离）。
// 复用自定义 DNS 拨号 transport；CheckRedirect 返回 ErrUseLastResponse（不自动跟随），
// 让 phpgo 按脚本 CURLOPT_FOLLOWLOCATION 逐请求控制重定向：
//   - FOLLOWLOCATION=false + CURLINFO_REDIRECT_URL 可拿到重定向地址（gitv.php 等依赖此语义）
//   - FOLLOWLOCATION=true 时 phpgo 在 defaultProxy 内用 Go 默认跟随逻辑
// 与代理专用 client 的区别：不带 m3u8 重定向统计/日志逻辑。
func NewPHPHTTPClient(c *config.Config) *http.Client {
	return &http.Client{
		Timeout:   c.HTTP.Timeout,
		Transport: newTransport(c),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("超出最大重定向次数 (%d 次)", maxRedirects)
			}
			return http.ErrUseLastResponse
		},
	}
}
