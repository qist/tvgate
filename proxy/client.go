package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	conf "github.com/qist/tvgate/proxy/config"
	httpclient "github.com/qist/tvgate/utils/http"
)

// Transport 缓存，避免每次请求都创建新的 http.Transport
var transportCache sync.Map

type transportCacheKey struct {
	proxyType   string
	proxyAddr   string
	enableIPv6  bool
	insecureTLS bool
}

// createProxyClient 根据代理配置和 IPv6 开关创建 http.Client
func CreateProxyClient(ctx context.Context, cfg *config.Config, proxyConfig config.ProxyConfig, enableIPv6 bool) (*http.Client, error) {
	NormalizeProxyConfig(&proxyConfig)

	proxyType := strings.ToLower(proxyConfig.Type)
	proxyAddr := fmt.Sprintf("%s:%d", proxyConfig.Server, proxyConfig.Port)

	// 检查缓存中是否有可用的 transport
	cacheKey := transportCacheKey{
		proxyType:   proxyType,
		proxyAddr:   proxyAddr,
		enableIPv6:  enableIPv6,
		insecureTLS: *cfg.HTTP.InsecureSkipVerify,
	}

	if cached, ok := transportCache.Load(cacheKey); ok {
		transport := cached.(*http.Transport)
		client := httpclient.NewHTTPClient(cfg, transport)
		return client, nil
	}

	transport := &http.Transport{
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: *cfg.HTTP.InsecureSkipVerify},
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
			// 通过代理拨号
			conn, err := dialer.DialContext(dialCtx, network, addr)
			if err != nil {
				return nil, fmt.Errorf("代理拨号失败: %w", err)
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
					return nil, fmt.Errorf("代理拨号失败: %w", err)
				}
				return conn, nil
			}

			// 没有代理，直接使用 SafeDialContext
			return safeDial(dialCtx, network, addr)
		}

	}
	// 缓存 transport 以便复用
	transportCache.Store(cacheKey, transport)

	// 这里使用 NewHTTPClient 生成最终 client
	client := httpclient.NewHTTPClient(cfg, transport)

	return client, nil
}

// CreateUniqueProxyClient 创建具有唯一性的代理客户端，避免连接复用问题
func CreateUniqueProxyClient(ctx context.Context, cfg *config.Config, proxyConfig config.ProxyConfig, enableIPv6 bool) (*http.Client, error) {
	// 使用CreateProxyClient创建基础客户端
	baseClient, err := CreateProxyClient(ctx, cfg, proxyConfig, enableIPv6)
	if err != nil {
		return nil, err
	}

	// 创建一个新的transport实例，以确保连接隔离
	transport := baseClient.Transport.(*http.Transport).Clone()

	// 设置更严格的连接管理参数，减少连接复用的可能性
	transport.MaxIdleConns = 1                   // 减少空闲连接数量
	transport.MaxIdleConnsPerHost = 1            // 每主机只保留一个空闲连接
	transport.IdleConnTimeout = 10 * time.Second // 更短的空闲连接超时时间
	transport.DisableKeepAlives = false          // 保持连接开启，但在必要时快速断开

	// 创建新的客户端，使用定制的传输层
	uniqueClient := &http.Client{
		Transport: transport,
		Timeout:   baseClient.Timeout,
	}

	return uniqueClient, nil
}
