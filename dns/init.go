package dns

import (
	"context"
	"net"
	"net/http"
	// "strings"
	"sync"
)

var onceInit sync.Once

func Init() {
	// 自动设置全局DNS解析器
	// 这样所有网络请求都会自动使用自定义DNS解析
	SetupGlobalDNSResolver()
}

func SetupGlobalDNSResolver() {
	onceInit.Do(func() {
		resolver := GetInstance()

		// 1️⃣ 替换全局 net.Resolver
		net.DefaultResolver = resolver.GetNetResolver()

		// 2️⃣ 替换全局 HTTP / TCP Dial
		http.DefaultTransport = &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}

				// 使用你的 Resolver 查 IP
				ips, err := resolver.LookupIP(host)
				if err != nil || len(ips) == 0 {
					return nil, err
				}

				// 取第一个 IP 连接
				return net.Dial(network, net.JoinHostPort(ips[0].String(), port))
			},
			// 可选：TLS 和其他配置保持默认
		}
	})
}
