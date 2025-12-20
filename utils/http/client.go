package http

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/qist/tvgate/cache"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/dns"
	"github.com/qist/tvgate/logger"
	"net"
	"net/http"
	"net/url"
)

const (
	maxRedirects = 10
)

func NewHTTPClient(c *config.Config, transport *http.Transport) *http.Client {
	// 获取自定义 Resolver 实例
	resolver := dns.GetInstance()
	if transport == nil {
		// 基础 dialer
		baseDialer := &net.Dialer{
			Timeout:   c.HTTP.ConnectTimeout,
			KeepAlive: c.HTTP.KeepAlive,
			DualStack: true,
		}

		// ✅ 自定义 DialContext，强制走 dns.GetInstance()
		transport = &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}

				// 用自定义 resolver 解析
				ips, err := resolver.LookupIPAddr(ctx, host)
				if err != nil || len(ips) == 0 {
					// logger.LogPrintf("⚠️ 自定义DNS解析失败 %s: %v, 尝试系统解析", host, err)
					return baseDialer.DialContext(ctx, network, addr) // fallback
				}

				ip := ips[0].IP.String()
				target := net.JoinHostPort(ip, port)
				// logger.LogPrintf("✅ DNS解析 %s -> %s", host, ip)

				return baseDialer.DialContext(ctx, network, target)
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

	return &http.Client{
		Timeout:   c.HTTP.Timeout,
		Transport: transport,
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
			req.URL = targetURL

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
