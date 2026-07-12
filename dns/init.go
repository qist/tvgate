package dns

import (
	"context"
	"net"
	"net/http"
	"time"
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

		// 2️⃣ 关闭旧的 DefaultTransport 连接池，避免内存泄漏
		if oldTransport, ok := http.DefaultTransport.(*http.Transport); ok {
			oldTransport.CloseIdleConnections()
		}

		// 3️⃣ 替换全局 HTTP / TCP Dial
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
			MaxIdleConns:        20,
			MaxIdleConnsPerHost: 2,
			IdleConnTimeout:     30 * time.Second,
		}
	})
}
