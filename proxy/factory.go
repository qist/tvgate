package proxy

import (
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/dns"
	cnf "github.com/qist/tvgate/proxy/config"
	"golang.org/x/net/proxy"
	"h12.io/socks"
)

// proxyServerDialer 用于连接代理服务器本身。
// 代理服务器可能是域名，这里必须走 TVGate 的 DNS 配置链（含 Android 系统解析兜底），
// 不能使用裸 net.Dialer 的系统 DNS。
func proxyServerDialer() *net.Dialer {
	return &net.Dialer{
		Timeout:  10 * time.Second,
		Resolver: dns.GetInstance().GetNetResolver(),
	}
}

func CreateProxyDialer(proxyConfig config.ProxyConfig) (*cnf.DialContextWrapper, error) {
	NormalizeProxyConfig(&proxyConfig)

	proxyAddr := fmt.Sprintf("%s:%d", proxyConfig.Server, proxyConfig.Port)
	proxyType := strings.ToLower(proxyConfig.Type)
	resolve := strings.ToLower(strings.TrimSpace(proxyConfig.Resolve))

	switch proxyType {
	case "socks5":
		var auth *proxy.Auth
		if proxyConfig.Username != "" {
			auth = &proxy.Auth{
				User:     proxyConfig.Username,
				Password: proxyConfig.Password,
			}
		}
		d, err := proxy.SOCKS5("tcp", proxyAddr, auth, proxyServerDialer())
		if err != nil {
			return nil, err
		}
		// d 已经是 proxy.Dialer 接口，直接赋值给 base
		return &cnf.DialContextWrapper{Base: d, Resolve: resolve}, nil

	case "socks4", "socks4a":
		dialFn := socks.Dial(fmt.Sprintf("%s://%s", proxyType, proxyAddr))
		if dialFn == nil {
			return nil, fmt.Errorf("创建 SOCKS4 拨号器失败")
		}
		// 这里需要包装成 proxy.Dialer 接口
		return &cnf.DialContextWrapper{
			Base:    &cnf.SocksDialerWrapper{DialFn: dialFn},
			Resolve: resolve,
		}, nil

	case "http", "https":
		headers := map[string]string{}
		if proxyConfig.Headers != nil {
			for k, v := range proxyConfig.Headers {
				headers[k] = v
			}
		}
		if proxyConfig.Username != "" && proxyConfig.Password != "" {
			auth := base64.StdEncoding.EncodeToString([]byte(proxyConfig.Username + ":" + proxyConfig.Password))
			headers["Proxy-Authorization"] = "Basic " + auth
		}
		// 仅当 headers 有内容时才启用 CONNECT 请求模式
		insecure := false
		if config.Cfg.HTTP.InsecureSkipVerify != nil {
			insecure = *config.Cfg.HTTP.InsecureSkipVerify
		}
		httpDialer := &cnf.HttpProxyDialer{
			ProxyAddr:          proxyAddr,
			Headers:            headers,
			Dialer:             proxyServerDialer(),
			TLS:                proxyType == "https",
			InsecureSkipVerify: insecure,
		}
		return &cnf.DialContextWrapper{Base: httpDialer, Resolve: resolve}, nil

	default:
		return nil, fmt.Errorf("不支持的代理类型: %s", proxyType)
	}
}
