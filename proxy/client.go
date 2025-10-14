package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	conf "github.com/qist/tvgate/proxy/config"
	httpclient "github.com/qist/tvgate/utils/http"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// createProxyClient 根据代理配置和 IPv6 开关创建 http.Client
func CreateProxyClient(ctx context.Context, cfg *config.Config, proxyConfig config.ProxyConfig, enableIPv6 bool) (*http.Client, error) {
	NormalizeProxyConfig(&proxyConfig)

	proxyType := strings.ToLower(proxyConfig.Type)
	proxyAddr := fmt.Sprintf("%s:%d", proxyConfig.Server, proxyConfig.Port)

	transport := &http.Transport{
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: false},
		ResponseHeaderTimeout: 10 * time.Second,
		IdleConnTimeout:       5 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   4,
		MaxConnsPerHost:       8,
		DisableKeepAlives:     false,
		DisableCompression:    true,
		ForceAttemptHTTP2:     false,
	}

	useStdProxyDialer := false

	if (proxyType == "http" || proxyType == "https") && proxyConfig.Username == "" && len(proxyConfig.Headers) == 0 {
		var proxyURL *url.URL
		var err error
		if proxyType == "https" {
			proxyURL, err = url.Parse("https://" + proxyAddr)
		} else {
			proxyURL, err = url.Parse("http://" + proxyAddr)
		}
		if err != nil {
			return nil, fmt.Errorf("解析代理地址失败: %v", err)
		}
		transport.Proxy = http.ProxyURL(proxyURL)
		logger.LogPrintf("使用标准 %s 代理方式: %s", proxyType, proxyURL.String())
		useStdProxyDialer = true
	}

	if !useStdProxyDialer {
		baseDialer, err := CreateProxyDialer(proxyConfig)
		if err != nil {
			return nil, fmt.Errorf("创建代理拨号器失败: %v", err)
		}

		dialer := &conf.DialContextWrapper{Base: baseDialer, EnableIPv6: enableIPv6}

		transport.DialContext = func(dialCtx context.Context, network, addr string) (net.Conn, error) {
			if !enableIPv6 && (network == "tcp6" || network == "tcp") {
				network = "tcp4"
			}
			// Step 1: 尝试通过代理拨号
			conn, err := dialer.DialContext(dialCtx, network, addr)
			if err != nil {
				// 代理拨号失败，直接返回
				return nil, fmt.Errorf("代理拨号失败: %w", err)
			}

			// Step 2: 拨号成功，但如果 Base 内部解析目标 host 失败，则在这里兜底
			// 例如 Base.Dial 返回某种 "host not found" 错误
			if isResolveError(err) {
				// SafeDialContext 兜底解析
				safeDial := conf.SafeDialContext(&net.Dialer{Timeout: 10 * time.Second}, enableIPv6)
				fmt.Printf("⚠️ 代理拨号成功，但目标解析失败 (%v)，尝试本地解析 %s...\n", err, addr)
				return safeDial(dialCtx, network, addr)
			}

			return conn, nil
		}
	} else {
		baseDialer := &net.Dialer{Timeout: 10 * time.Second}
		safeDial := conf.SafeDialContext(baseDialer, enableIPv6)

		transport.DialContext = func(dialCtx context.Context, network, addr string) (net.Conn, error) {
			if !enableIPv6 && (network == "tcp6" || network == "tcp") {
				network = "tcp4"
			}

			if transport.Proxy != nil {
				// 尝试通过代理拨号
				conn, err := baseDialer.DialContext(dialCtx, network, addr)
				if err != nil {
					// 代理拨号失败，直接返回
					return nil, fmt.Errorf("代理拨号失败: %w", err)
				}

				// 拨号成功，但如果解析失败，可以在这里判断
				if isResolveError(err) {
					// 目标 host 解析失败 → fallback 本地解析
					fmt.Printf("⚠️ 代理拨号成功但目标解析失败 %s, 使用本地解析...\n", addr)
					return safeDial(dialCtx, network, addr)
				}

				return conn, nil
			}

			// 没有代理，直接使用 SafeDialContext
			return safeDial(dialCtx, network, addr)
		}

	}
	// 这里使用 NewHTTPClient 生成最终 client
	client := httpclient.NewHTTPClient(cfg, transport)

	return client, nil
}

// 判断错误是否属于解析失败
func isResolveError(err error) bool {
	if err == nil {
		return false
	}

	// 如果是 net.DNSError
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}

	// 某些拨号器可能返回字符串错误
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "no such host") || strings.Contains(msg, "unknown host") {
		return true
	}

	return false
}
