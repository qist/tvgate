package config

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"

	"net"
	"net/http"
	"strings"

	// "sync"
	"github.com/qist/tvgate/dns"
	tsync "github.com/qist/tvgate/utils/sync"

	// "github.com/qist/tvgate/logger"
	"time"

	"golang.org/x/net/proxy"
)

type DialContextWrapper struct {
	Base       proxy.Dialer
	EnableIPv6 bool
	// Resolve: remote(默认) 域名原样交给代理；local 先用 TVGate 的 DNS 解析成 IP。
	Resolve string
}

func (d *DialContextWrapper) Dial(network, addr string) (net.Conn, error) {
	// 兼容 proxy.Dialer 接口，调用 DialContext，传入背景 Context
	return d.DialContext(context.Background(), network, addr)
}

// // 定义dialResult结构体用于sync.Pool
// type dialResult struct {
// 	conn net.Conn
// 	err  error
// }

// 创建sync.Pool用于复用dialResult对象
//
//	var dialResultPool = sync.Pool{
//		New: func() interface{} {
//			return &dialResult{}
//		},
//	}
func (d *DialContextWrapper) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	// 创建带超时的 context，避免连接永远挂起
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// local: TVGate 先解析目标域名，再把 IP 交给代理。
	// remote(默认): addr 原样交给代理，由代理端解析。
	if strings.EqualFold(strings.TrimSpace(d.Resolve), "local") {
		if host, port, err := net.SplitHostPort(addr); err == nil && net.ParseIP(host) == nil {
			conn, err := d.dialWithLocalDNS(dialCtx, network, host, port)
			if err != nil {
				return nil, err
			}
			return conn, nil
		}
	}

	return d.dialBase(dialCtx, network, addr)
}

// dialWithLocalDNS 使用 TVGate 的 dns 单例解析目标，并按 IPv4/IPv6 开关过滤后依次尝试。
func (d *DialContextWrapper) dialWithLocalDNS(ctx context.Context, network, host, port string) (net.Conn, error) {
	resolver := dns.GetInstance()
	var (
		ips []net.IPAddr
		err error
	)
	if d.EnableIPv6 {
		ips, err = resolver.LookupIPAddr(ctx, host)
	} else {
		ips, err = dns.LookupIPWithIPv6Disabled(ctx, host)
	}
	if err != nil {
		return nil, fmt.Errorf("本地 DNS 解析 %s 失败: %w", host, err)
	}

	resolvedNetwork := network
	if !d.EnableIPv6 && (resolvedNetwork == "tcp6" || resolvedNetwork == "tcp") {
		resolvedNetwork = "tcp4"
	}

	var lastErr error
	tried := 0
	for _, ip := range ips {
		if ip.IP == nil {
			continue
		}
		if resolvedNetwork == "tcp4" && ip.IP.To4() == nil {
			continue
		}
		if resolvedNetwork == "tcp6" && ip.IP.To4() != nil {
			continue
		}
		tried++
		conn, err := d.dialBase(ctx, resolvedNetwork, net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	if lastErr == nil {
		if tried == 0 {
			return nil, fmt.Errorf("本地 DNS 解析 %s 没有匹配 %s 的地址", host, resolvedNetwork)
		}
		lastErr = fmt.Errorf("本地 DNS 解析 %s 没有可用地址", host)
	}
	return nil, lastErr
}

// dialBase 执行实际拨号，兼容实现了 proxy.ContextDialer 的上游 dialer。
func (d *DialContextWrapper) dialBase(dialCtx context.Context, network, addr string) (net.Conn, error) {
	// 优先使用 ContextDialer 接口，避免创建额外的 Goroutine
	if cd, ok := d.Base.(proxy.ContextDialer); ok {
		return cd.DialContext(dialCtx, network, addr)
	}

	// 根据 IPv6 设置调整网络类型
	if !d.EnableIPv6 && (network == "tcp6" || network == "tcp") {
		network = "tcp4"
	}

	// 每次新建 dialResult，避免复用导致的数据竞争
	type dialResult struct {
		conn net.Conn
		err  error
	}
	resultChan := make(chan dialResult, 1)

	// 异步拨号
	var wg tsync.WaitGroup
	wg.Go(func() {
		conn, err := d.Base.Dial(network, addr)
		resultChan <- dialResult{conn, err}
	})

	select {
	case <-dialCtx.Done():
		go func() {
			res := <-resultChan
			if res.conn != nil {
				res.conn.Close()
			}
		}()
		return nil, dialCtx.Err()
	case res := <-resultChan:
		return res.conn, res.err
	}
}

// httpProxyDialer 实现 proxy.Dialer 接口
type HttpProxyDialer struct {
	ProxyAddr string
	Headers   map[string]string
	// Dialer 用于连接代理服务器；为空时退回 net.DialTimeout。
	// 由 proxy.CreateProxyDialer 注入带 DNS 配置的 dialer。
	Dialer *net.Dialer
	// TLS 为 true 时表示代理本身是 https（先与代理建 TLS，再发 CONNECT）。
	TLS                bool
	InsecureSkipVerify bool
}

func (d *HttpProxyDialer) Dial(network, addr string) (net.Conn, error) {
	dialer := d.Dialer
	if dialer == nil {
		dialer = &net.Dialer{Timeout: 10 * time.Second}
	}
	// 连接代理服务器
	var (
		conn net.Conn
		err  error
	)
	if d.TLS {
		conn, err = tls.DialWithDialer(dialer, "tcp", d.ProxyAddr, &tls.Config{
			InsecureSkipVerify: d.InsecureSkipVerify,
		})
	} else {
		conn, err = dialer.Dial("tcp", d.ProxyAddr)
	}
	if err != nil {
		return nil, fmt.Errorf("连接代理失败: %v", err)
	}

	// 构造 CONNECT 请求
	var req strings.Builder
	req.WriteString(fmt.Sprintf("CONNECT %s HTTP/1.1\r\n", addr))

	if len(d.Headers) > 0 {
		// 写入自定义请求头
		for k, v := range d.Headers {
			req.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
		}
	} else {
		// 没有自定义头，写默认 Host
		req.WriteString(fmt.Sprintf("Host: %s\r\n", addr))
	}
	req.WriteString("\r\n")

	// 发送 CONNECT 请求
	if _, err := conn.Write([]byte(req.String())); err != nil {
		conn.Close()
		return nil, fmt.Errorf("发送 CONNECT 请求失败: %v", err)
	}

	// 读取响应
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("读取 CONNECT 响应失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("HTTP 代理 CONNECT 失败: %s", resp.Status)
	}

	return conn, nil
}

type SocksDialerWrapper struct {
	DialFn func(network, addr string) (net.Conn, error)
}

func (w *SocksDialerWrapper) Dial(network, addr string) (net.Conn, error) {
	return w.DialFn(network, addr)
}

// SafeDialContext 封装 DNS fallback 的拨号逻辑
func SafeDialContext(base *net.Dialer, enableIPv6 bool) func(ctx context.Context, network, addr string) (net.Conn, error) {
	resolver := dns.GetInstance()

	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if !enableIPv6 && (network == "tcp6" || network == "tcp") {
			network = "tcp4"
		}

		host, port, err := net.SplitHostPort(addr)
		if err == nil {
			var ips []net.IPAddr
			// 根据IPv6设置选择解析函数
			if !enableIPv6 {
				ips, err = dns.LookupIPWithIPv6Disabled(ctx, host)
			} else {
				ips, err = resolver.LookupIPAddr(ctx, host)
			}

			if err == nil && len(ips) > 0 {
				ip := ips[0].IP.String()
				target := net.JoinHostPort(ip, port)
				// logger.LogPrintf("✅ 自定义DNS解析 %s -> %s", host, ip)
				return base.DialContext(ctx, network, target)
			}
			// logger.LogPrintf("⚠️ DNS解析失败 %s: %v, 回落系统DNS", host, err)
		}

		// 系统 DNS
		return base.DialContext(ctx, network, addr)
	}
}
