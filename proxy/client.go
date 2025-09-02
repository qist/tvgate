package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	httpclient "github.com/qist/tvgate/utils/http"
	conf "github.com/qist/tvgate/proxy/config"
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
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
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

		dialer := &conf.DialContextWrapper{Base: baseDialer}
		transport.DialContext = func(dialCtx context.Context, network, addr string) (net.Conn, error) {
			if !enableIPv6 && (network == "tcp6" || network == "tcp") {
				network = "tcp4"
			}
			return dialer.DialContext(dialCtx, network, addr)
		}
	} else {
		// 标准库代理也要控制 IPv6
		baseDialer := &net.Dialer{
			Timeout: 10 * time.Second,
		}
		origDial := transport.DialContext
		transport.DialContext = func(dialCtx context.Context, network, addr string) (net.Conn, error) {
			if !enableIPv6 && (network == "tcp6" || network == "tcp") {
				network = "tcp4"
			}
			if origDial != nil {
				return origDial(dialCtx, network, addr)
			}
			return baseDialer.DialContext(dialCtx, network, addr)
		}
	}

	// 这里使用 NewHTTPClient 生成最终 client
	client := httpclient.NewHTTPClient(cfg, transport)

	return client, nil
}
