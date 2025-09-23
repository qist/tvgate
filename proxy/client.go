package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/dns"
	"github.com/qist/tvgate/logger"
	conf "github.com/qist/tvgate/proxy/config"
	httpclient "github.com/qist/tvgate/utils/http"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CreateProxyClient 根据代理配置和 IPv6 开关创建 http.Client
func CreateProxyClient(ctx context.Context, cfg *config.Config, proxyConfig config.ProxyConfig, enableIPv6 bool) (*http.Client, error) {
	// 标准化代理配置
	NormalizeProxyConfig(&proxyConfig)

	proxyType := strings.ToLower(proxyConfig.Type)
	proxyAddr := fmt.Sprintf("%s:%d", proxyConfig.Server, proxyConfig.Port)

	// 自定义 DNS 单例
	resolver := dns.GetInstance()

	// 基础拨号器
	baseDialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 10 * time.Second,
	}

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

	// -------- 标准 HTTP/HTTPS 代理 --------
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

	var dialer conf.DialContextWrapper
	if !useStdProxyDialer {
		// 非标准库代理，创建自定义拨号器
		baseDialerProxy, err := CreateProxyDialer(proxyConfig)
		if err != nil {
			return nil, fmt.Errorf("创建代理拨号器失败: %v", err)
		}
		dialer = conf.DialContextWrapper{Base: baseDialerProxy}
	}

	// -------- 自定义 DialContext --------
	transport.DialContext = func(dialCtx context.Context, network, addr string) (net.Conn, error) {
		// IPv6 控制
		if !enableIPv6 && (network == "tcp6" || network == "tcp") {
			network = "tcp4"
		}

		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}

		// 1️⃣ 自定义 DNS 解析
		ips, err := resolver.LookupIPAddr(dialCtx, host)
		targetAddr := addr
		if err == nil && len(ips) > 0 {
			ip := ips[0].IP.String()
			targetAddr = net.JoinHostPort(ip, port)
			logger.LogPrintf("✅ DNS解析 %s -> %s", host, ip)
		} else {
			logger.LogPrintf("⚠️ 自定义DNS解析失败 %s: %v, 回落系统DNS", host, err)
		}

		// 2️⃣ 选择拨号器
		if !useStdProxyDialer {
			return dialer.DialContext(dialCtx, network, targetAddr)
		}

		// 3️⃣ 标准库拨号器
		return baseDialer.DialContext(dialCtx, network, targetAddr)
	}

	// -------- 生成最终 HTTP Client --------
	client := httpclient.NewHTTPClient(cfg, transport)
	return client, nil
}
