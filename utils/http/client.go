package http

import (
	"crypto/tls"
	"fmt"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/cache"
	"net"
	"net/http"
	"net/url"
)

const (
	// bufferThreshold = 256 * 1024 // 先缓冲64KB
	maxRedirects = 10 // 最大重定向次数
)

func NewHTTPClient(c *config.Config, transport *http.Transport) *http.Client {
	if transport == nil {
		transport = &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   c.HTTP.ConnectTimeout,
				KeepAlive: c.HTTP.KeepAlive,
				DualStack: true,
			}).DialContext,
			ResponseHeaderTimeout: c.HTTP.ResponseHeaderTimeout,
			TLSClientConfig:       &tls.Config{},
			IdleConnTimeout:       c.HTTP.IdleConnTimeout,
			TLSHandshakeTimeout:   c.HTTP.TLSHandshakeTimeout,
			ExpectContinueTimeout: c.HTTP.ExpectContinueTimeout,
			MaxIdleConns:          c.HTTP.MaxIdleConns,
			MaxIdleConnsPerHost:   c.HTTP.MaxIdleConnsPerHost,
			MaxConnsPerHost:       c.HTTP.MaxConnsPerHost,
			DisableKeepAlives:     c.HTTP.DisableKeepAlives,
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

			// ✅ 添加记录重定向信息
			if transport != nil {
				origin := previousURL.Hostname()
				target := targetURL.Hostname()
				if origin != "" && target != "" {
					cache.AddRedirectIP(origin, target)
				}
			}
			logger.LogPrintf("从 %s 重定向到 %s", previousURL, targetURL)

			// 不自动跟随重定向
			return http.ErrUseLastResponse
		},
	}
}
