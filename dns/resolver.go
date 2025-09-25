package dns

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ameshkov/dnscrypt/v2"
	"github.com/jedisct1/go-dnsstamps"
	"github.com/miekg/dns"
	"github.com/qist/tvgate/config"
	// "github.com/qist/tvgate/logger"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// --------------------------- DNS 客户端接口 ---------------------------

type dnsClient interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

type plainDNSClient struct {
	server  string
	timeout time.Duration
}

type dohClient struct {
	server  string
	timeout time.Duration
}

type doh3Client struct {
	server  string
	timeout time.Duration
}

type dnsCryptClient struct {
	server string
}

type dotClient struct {
	server  string
	timeout time.Duration
}

type Common struct {
	Server    string
	ReuseConn bool
}

// QUIC 是用于通过 QUIC 协议进行 DNS 查询的结构体
type QUIC struct {
	Common
	TLSConfig       *tls.Config
	PMTUD           bool
	AddLengthPrefix bool

	// conn *quic.Conn
}

// quicClient 是我们项目中使用的QUIC客户端
type quicClient struct {
	server  string
	timeout time.Duration
}

// --------------------------- DNS 解析器 ---------------------------

type Resolver struct {
	clients        []dnsClient
	systemResolver *net.Resolver
	resolvers      []string
	timeout        time.Duration
	mutex          sync.RWMutex
}

var (
	instance *Resolver
	once     sync.Once
)

// GetInstance 获取单例 Resolver
func GetInstance() *Resolver {
	once.Do(func() {
		instance = &Resolver{
			systemResolver: &net.Resolver{
				PreferGo:     true,
				StrictErrors: false,
			},
		}
		instance.loadConfig()
	})
	return instance
}

// --------------------------- 配置加载 ---------------------------

func (r *Resolver) loadConfig() {
	cfg := config.Cfg

	r.mutex.Lock()
	defer r.mutex.Unlock()

	r.timeout = cfg.DNS.Timeout
	if r.timeout == 0 {
		r.timeout = 5 * time.Second
	}

	if len(cfg.DNS.Servers) > 0 {
		r.resolvers = make([]string, len(cfg.DNS.Servers))
		copy(r.resolvers, cfg.DNS.Servers)

		r.clients = make([]dnsClient, 0, len(cfg.DNS.Servers))
		for _, resolver := range cfg.DNS.Servers {
			client, err := createDNSClient(resolver, r.timeout)
			if err == nil {
				r.clients = append(r.clients, client)
			} else {
				fmt.Printf("WARNING: Failed to create DNS client for %s: %v\n", resolver, err)
			}
		}
	} else {
		r.clients = []dnsClient{}
		r.resolvers = []string{}
	}
}

// createDNSClient 根据服务器类型创建客户端
func createDNSClient(server string, timeout time.Duration) (dnsClient, error) {
	if isDNSStamp(server) {
		stamp, err := dnsstamps.NewServerStampFromString(server)
		if err != nil {
			return nil, err
		}
		switch stamp.Proto {
		case dnsstamps.StampProtoTypeDNSCrypt:
			return &dnsCryptClient{server: server}, nil
		case dnsstamps.StampProtoTypeDoH:
			return &dohClient{server: "https://" + stamp.ProviderName, timeout: timeout}, nil
		case dnsstamps.StampProtoTypeTLS:
			return &dotClient{server: stamp.ServerAddrStr, timeout: timeout}, nil
		case dnsstamps.StampProtoTypeDoQ:
			return &quicClient{server: stamp.ServerAddrStr, timeout: timeout}, nil
		default:
			return &plainDNSClient{server: stamp.ServerAddrStr, timeout: timeout}, nil
		}
	}

	parsedURL, err := url.Parse(server)
	if err == nil {
		switch parsedURL.Scheme {
		case "https":
			return &dohClient{server: server, timeout: timeout}, nil
		case "h3":
			host := strings.TrimPrefix(server, "h3://")
			return &doh3Client{server: "https://" + host, timeout: timeout}, nil
		case "sdns":
			return &dnsCryptClient{server: server}, nil
		case "tls":
			// DoT客户端
			host := strings.TrimPrefix(server, "tls://")
			return &dotClient{server: host, timeout: timeout}, nil
		case "quic":
			// QUIC客户端
			host := strings.TrimPrefix(server, "quic://")
			return &quicClient{server: host, timeout: timeout}, nil
		case "tcp", "udp":
			return &plainDNSClient{server: server, timeout: timeout}, nil
		}
	}

	return &plainDNSClient{server: server, timeout: timeout}, nil
}

func isDNSStamp(server string) bool {
	return len(server) > 10 && strings.HasPrefix(server, "sdns://")
}

// --------------------------- 查询接口 ---------------------------

func (r *Resolver) LookupIP(host string) ([]net.IP, error) {
	r.mutex.RLock()
	timeout := r.timeout
	clients := r.clients
	r.mutex.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// 首先尝试配置的DNS解析器
	for _, client := range clients {
		ips, err := client.LookupIPAddr(ctx, host)
		if err == nil {
			result := make([]net.IP, len(ips))
			for i, ip := range ips {
				result[i] = ip.IP
			}
			return result, nil
		}
		// 即使有错误也继续尝试下一个解析器
	}

	// 如果配置的DNS解析失败或超时，回退到系统解析器
	// 创建一个新的context，不带超时，让系统解析器自己处理
	systemCtx := context.Background()
	ips, err := r.systemResolver.LookupIPAddr(systemCtx, host)
	if err != nil {
		return nil, fmt.Errorf("both configured and system DNS resolvers failed: %v", err)
	}

	result := make([]net.IP, len(ips))
	for i, ip := range ips {
		result[i] = ip.IP
	}
	return result, nil
}

func (r *Resolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	r.mutex.RLock()
	timeout := r.timeout
	clients := r.clients
	r.mutex.RUnlock()

	// 创建带超时的上下文
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 首先尝试配置的DNS解析器
	for _, client := range clients {
		ips, err := client.LookupIPAddr(ctx, host)
		if err == nil {
			return ips, nil
		}
		// 即使有错误也继续尝试下一个解析器
	}

	// 如果配置的DNS解析失败或超时，回退到系统解析器
	// 使用原始context（可能不带超时）来调用系统解析器
	ips, err := r.systemResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("both configured and system DNS resolvers failed: %v", err)
	}

	return ips, nil
}

// RefreshConfig 刷新配置
func (r *Resolver) RefreshConfig() {
	r.loadConfig()
}

// GetResolvers 获取当前 DNS 列表
func (r *Resolver) GetResolvers() []string {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	resolvers := make([]string, len(r.resolvers))
	copy(resolvers, r.resolvers)
	return resolvers
}

// --------------------------- Dialer ---------------------------

func (r *Resolver) GetDialer() *net.Dialer {
	r.mutex.RLock()
	timeout := r.timeout
	r.mutex.RUnlock()

	return &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
		Resolver:  r.GetNetResolver(),
	}
}

func (r *Resolver) GetFallbackDialer() *net.Dialer {
	r.mutex.RLock()
	timeout := r.timeout
	r.mutex.RUnlock()

	return &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
		Resolver:  r.systemResolver,
	}
}

func (r *Resolver) createResolverFromClients(clients []dnsClient) *net.Resolver {
	if len(clients) == 0 {
		return r.systemResolver
	}

	r.mutex.RLock()
	timeout := r.timeout
	r.mutex.RUnlock()

	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			// 创建带超时的上下文
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			// 尝试每个配置的客户端，但只处理plainDNSClient类型
			// DoH、DoT、DNSCrypt等加密DNS协议不能通过Dial函数处理
			for _, client := range clients {
				if plainClient, ok := client.(*plainDNSClient); ok {
					// 处理传统DNS客户端
					serverAddr := plainClient.server
					if !strings.Contains(serverAddr, ":") {
						if strings.HasPrefix(plainClient.server, "tls://") {
							// DoT默认端口是853
							serverAddr += ":853"
						} else {
							// UDP/TCP默认端口是53
							serverAddr += ":53"
						}
					}

					// 移除协议前缀
					serverAddr = strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(serverAddr, "udp://"), "tcp://"), "tls://")

					d := &net.Dialer{Timeout: timeout}
					conn, err := d.DialContext(ctx, network, serverAddr)
					if err == nil {
						return conn, nil
					}
				}
			}

			// 如果没有plainDNSClient或者都失败了，使用系统解析器
			d := &net.Dialer{Timeout: timeout}
			return d.DialContext(ctx, network, address)
		},
	}
}

// --------------------------- 解析器获取 ---------------------------

func (r *Resolver) GetNetResolver() *net.Resolver {
	r.mutex.RLock()
	clients := r.clients
	r.mutex.RUnlock()
	return r.createResolverFromClients(clients)
}

func (r *Resolver) GetSystemResolver() *net.Resolver {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	return r.systemResolver
}

// --------------------------- 具体客户端实现 ---------------------------

// 解析响应
func parseDNSResponse(respBytes []byte) ([]net.IPAddr, error) {
	respMsg := &dns.Msg{}
	if err := respMsg.Unpack(respBytes); err != nil {
		return nil, fmt.Errorf("解包DNS响应失败: %w", err)
	}

	// 检查响应是否为错误
	if respMsg.Rcode != dns.RcodeSuccess {
		return nil, fmt.Errorf("DNS查询失败，响应码: %d", respMsg.Rcode)
	}

	var addrs []net.IPAddr
	for _, answer := range respMsg.Answer {
		switch v := answer.(type) {
		case *dns.A:
			addrs = append(addrs, net.IPAddr{IP: v.A})
		case *dns.AAAA:
			addrs = append(addrs, net.IPAddr{IP: v.AAAA})
		}
	}

	// 如果没有解析到地址，返回相应的错误
	if len(addrs) == 0 {
		return nil, fmt.Errorf("DNS查询成功但未返回任何IP地址")
	}
	// logger.LogPrintf("DNS query via %v returned %d addresses", respMsg.Question, len(addrs))
	return addrs, nil
}

// --------------------------- plainDNSClient ---------------------------

func (c *plainDNSClient) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	// 创建DNS查询
	msg := &dns.Msg{}
	msg.SetQuestion(dns.Fqdn(host), dns.TypeA)

	// 添加EDNS0记录支持
	// EDNS0允许更大的UDP数据包（4096字节），并支持DNSSEC等扩展功能
	// 同时添加EDNS Client Subnet (ECS)选项，发送TVGate服务器所在IP地址
	localIP, err := getLocalIP()
	if err == nil && localIP != nil {
		// 创建ECS选项
		ecs := &dns.EDNS0_SUBNET{
			Code:          dns.EDNS0SUBNET,
			Family:        1, // IPv4
			SourceNetmask: 24,
			SourceScope:   0,
			Address:       localIP,
		}

		// 创建OPT记录并添加ECS选项
		opt := &dns.OPT{
			Hdr: dns.RR_Header{
				Name:   ".",
				Rrtype: dns.TypeOPT,
			},
			Option: []dns.EDNS0{ecs},
		}

		// 将OPT记录添加到DNS查询的额外记录部分
		msg.Extra = append(msg.Extra, opt)
	} else {
		// 如果无法获取本地IP，则只使用基本的EDNS0功能
		msg.SetEdns0(4096, false)
	}

	// 将查询打包成字节
	queryBytes, err := msg.Pack()
	if err != nil {
		return nil, fmt.Errorf("打包DNS查询失败: %w", err)
	}

	// 发送DNS查询
	respBytes, err := executeDNSQuery(ctx, c.server, queryBytes, c.timeout)
	if err != nil {
		return nil, fmt.Errorf("DNS查询失败: %w", err)
	}

	// 解析响应
	addrs, err := parseDNSResponse(respBytes)
	if err != nil {
		return nil, fmt.Errorf("解析DNS响应失败: %w", err)
	}

	return addrs, nil
}

// getLocalIP 获取本地IP地址，用于EDNS Client Subnet (ECS)
func getLocalIP() (net.IP, error) {
	// 准备多个备选地址
	dnsServers := []string{"223.5.5.5:80", "223.6.6.6:80", "119.29.29.29:80"}

	for _, server := range dnsServers {
		conn, err := net.DialTimeout("udp", server, 2*time.Second)
		if err != nil {
			continue // 当前服务器失败，尝试下一个
		}
		defer conn.Close()
		localAddr := conn.LocalAddr().(*net.UDPAddr)
		return localAddr.IP, nil
	}
	return nil, fmt.Errorf("could not connect to any DNS server")
}

// --------------------------- dohClient ---------------------------

func (c *dohClient) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	// 创建DNS查询
	msg := &dns.Msg{}
	msg.SetQuestion(dns.Fqdn(host), dns.TypeA)
	// 添加EDNS0记录支持
	// EDNS0允许更大的UDP数据包（4096字节），并支持DNSSEC等扩展功能
	// 同时添加EDNS Client Subnet (ECS)选项，发送TVGate服务器所在IP地址
	localIP, err := getLocalIP()
	if err == nil && localIP != nil {
		// 创建ECS选项
		ecs := &dns.EDNS0_SUBNET{
			Code:          dns.EDNS0SUBNET,
			Family:        1, // IPv4
			SourceNetmask: 24,
			SourceScope:   0,
			Address:       localIP,
		}

		// 创建OPT记录并添加ECS选项
		opt := &dns.OPT{
			Hdr: dns.RR_Header{
				Name:   ".",
				Rrtype: dns.TypeOPT,
			},
			Option: []dns.EDNS0{ecs},
		}

		// 将OPT记录添加到DNS查询的额外记录部分
		msg.Extra = append(msg.Extra, opt)
	} else {
		// 如果无法获取本地IP，则只使用基本的EDNS0功能
		msg.SetEdns0(4096, false)
	}

	// 将查询打包成字节
	queryBytes, err := msg.Pack()
	if err != nil {
		return nil, fmt.Errorf("打包DNS查询失败: %w", err)
	}

	// 使用base64编码查询
	b64 := base64.RawURLEncoding.EncodeToString(queryBytes)

	// 发送DoH查询
	respBytes, err := sendDoHQuery(ctx, c.server, b64, c.timeout)
	if err != nil {
		return nil, fmt.Errorf("DoH查询失败: %w", err)
	}

	// 解析响应
	addrs, err := parseDNSResponse(respBytes)
	if err != nil {
		return nil, fmt.Errorf("解析DoH响应失败: %w", err)
	}

	return addrs, nil
}

// --------------------------- doh3Client ---------------------------

func (c *doh3Client) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	// 创建DNS查询
	msg := &dns.Msg{}
	msg.SetQuestion(dns.Fqdn(host), dns.TypeA)
	// 添加EDNS0记录支持
	// EDNS0允许更大的UDP数据包（4096字节），并支持DNSSEC等扩展功能
	// 同时添加EDNS Client Subnet (ECS)选项，发送TVGate服务器所在IP地址
	localIP, err := getLocalIP()
	if err == nil && localIP != nil {
		// 创建ECS选项
		ecs := &dns.EDNS0_SUBNET{
			Code:          dns.EDNS0SUBNET,
			Family:        1, // IPv4
			SourceNetmask: 24,
			SourceScope:   0,
			Address:       localIP,
		}

		// 创建OPT记录并添加ECS选项
		opt := &dns.OPT{
			Hdr: dns.RR_Header{
				Name:   ".",
				Rrtype: dns.TypeOPT,
			},
			Option: []dns.EDNS0{ecs},
		}

		// 将OPT记录添加到DNS查询的额外记录部分
		msg.Extra = append(msg.Extra, opt)
	} else {
		// 如果无法获取本地IP，则只使用基本的EDNS0功能
		msg.SetEdns0(4096, false)
	}

	// 将查询打包成字节
	queryBytes, err := msg.Pack()
	if err != nil {
		return nil, fmt.Errorf("打包DNS查询失败: %w", err)
	}

	// 使用base64编码查询
	b64 := base64.RawURLEncoding.EncodeToString(queryBytes)

	// 发送DoH3查询
	respBytes, err := sendH3Query(ctx, c.server, b64, c.timeout)
	if err != nil {
		return nil, fmt.Errorf("DoH3查询失败: %w", err)
	}

	// 解析响应
	addrs, err := parseDNSResponse(respBytes)
	if err != nil {
		return nil, fmt.Errorf("解析DoH3响应失败: %w", err)
	}

	return addrs, nil
}

// --------------------------- dnsCryptClient ---------------------------

func (c *dnsCryptClient) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	// 创建DNS查询
	msg := &dns.Msg{}
	msg.SetQuestion(dns.Fqdn(host), dns.TypeA)
	// 添加EDNS0记录支持
	// EDNS0允许更大的UDP数据包（4096字节），并支持DNSSEC等扩展功能
	// 同时添加EDNS Client Subnet (ECS)选项，发送TVGate服务器所在IP地址
	localIP, err := getLocalIP()
	if err == nil && localIP != nil {
		// 创建ECS选项
		ecs := &dns.EDNS0_SUBNET{
			Code:          dns.EDNS0SUBNET,
			Family:        1, // IPv4
			SourceNetmask: 24,
			SourceScope:   0,
			Address:       localIP,
		}

		// 创建OPT记录并添加ECS选项
		opt := &dns.OPT{
			Hdr: dns.RR_Header{
				Name:   ".",
				Rrtype: dns.TypeOPT,
			},
			Option: []dns.EDNS0{ecs},
		}

		// 将OPT记录添加到DNS查询的额外记录部分
		msg.Extra = append(msg.Extra, opt)
	} else {
		// 如果无法获取本地IP，则只使用基本的EDNS0功能
		msg.SetEdns0(4096, false)
	}

	// 将查询打包成字节
	queryBytes, err := msg.Pack()
	if err != nil {
		return nil, fmt.Errorf("打包DNS查询失败: %w", err)
	}

	// 使用base64编码查询
	b64 := base64.RawURLEncoding.EncodeToString(queryBytes)

	// 发送DNSCrypt查询
	addrs, err := sendCryptQuery(ctx, c.server, b64)
	if err != nil {
		return nil, fmt.Errorf("DNSCrypt查询失败: %w", err)
	}

	return addrs, nil
}

// --------------------------- dotClient ---------------------------

func (c *dotClient) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	// 创建DNS查询
	msg := &dns.Msg{}
	msg.SetQuestion(dns.Fqdn(host), dns.TypeA)
	// 添加EDNS0记录支持
	// EDNS0允许更大的UDP数据包（4096字节），并支持DNSSEC等扩展功能
	// 同时添加EDNS Client Subnet (ECS)选项，发送TVGate服务器所在IP地址
	localIP, err := getLocalIP()
	if err == nil && localIP != nil {
		// 创建ECS选项
		ecs := &dns.EDNS0_SUBNET{
			Code:          dns.EDNS0SUBNET,
			Family:        1, // IPv4
			SourceNetmask: 24,
			SourceScope:   0,
			Address:       localIP,
		}

		// 创建OPT记录并添加ECS选项
		opt := &dns.OPT{
			Hdr: dns.RR_Header{
				Name:   ".",
				Rrtype: dns.TypeOPT,
			},
			Option: []dns.EDNS0{ecs},
		}

		// 将OPT记录添加到DNS查询的额外记录部分
		msg.Extra = append(msg.Extra, opt)
	} else {
		// 如果无法获取本地IP，则只使用基本的EDNS0功能
		msg.SetEdns0(4096, false)
	}

	// 将查询打包成字节
	queryBytes, err := msg.Pack()
	if err != nil {
		return nil, fmt.Errorf("打包DNS查询失败: %w", err)
	}

	// 确保服务器地址包含端口
	serverAddr := c.server
	if !strings.Contains(serverAddr, ":") {
		serverAddr += ":853" // DoT默认端口
	}

	// 创建带超时的上下文
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	// 创建TLS连接
	dialer := &net.Dialer{Timeout: c.timeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", serverAddr, &tls.Config{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return nil, fmt.Errorf("连接DoT服务器失败: %w", err)
	}
	defer conn.Close()

	// 设置读取超时
	deadline, ok := ctx.Deadline()
	if ok {
		conn.SetReadDeadline(deadline)
	}

	// 发送查询 - DoT使用TCP，需要发送2字节的长度字段
	lengthField := make([]byte, 2)
	lengthField[0] = byte(len(queryBytes) >> 8)
	lengthField[1] = byte(len(queryBytes) & 0xff)

	if _, err := conn.Write(lengthField); err != nil {
		return nil, fmt.Errorf("发送长度字段失败: %w", err)
	}

	if _, err := conn.Write(queryBytes); err != nil {
		return nil, fmt.Errorf("发送查询失败: %w", err)
	}

	// 读取响应
	resp := make([]byte, 4096)

	n, err := conn.Read(resp)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	// 重置读取超时，避免影响后续操作
	conn.SetReadDeadline(time.Time{})

	// DoT使用TCP，需要读取长度字段并确保读取完整响应
	if n < 2 {
		return nil, fmt.Errorf("响应太短，无法读取长度字段")
	}

	// 读取长度字段
	respLen := int(resp[0])<<8 | int(resp[1])
	if respLen > len(resp)-2 {
		return nil, fmt.Errorf("响应长度超过缓冲区大小")
	}

	// 如果还没有读取完整响应，继续读取
	for n-2 < respLen {
		nn, err := conn.Read(resp[n:])
		if err != nil {
			return nil, fmt.Errorf("读取响应失败: %w", err)
		}
		n += nn
	}

	// 解析响应
	addrs, err := parseDNSResponse(resp[2 : 2+respLen])
	if err != nil {
		return nil, fmt.Errorf("解析DoT响应失败: %w", err)
	}

	return addrs, nil
}

// --------------------------- quicClient ---------------------------

func (c *quicClient) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	// 创建DNS查询
	msg := &dns.Msg{}
	msg.SetQuestion(dns.Fqdn(host), dns.TypeA)
	// 添加EDNS0记录支持
	// EDNS0允许更大的UDP数据包（4096字节），并支持DNSSEC等扩展功能
	// 同时添加EDNS Client Subnet (ECS)选项，发送TVGate服务器所在IP地址
	localIP, err := getLocalIP()
	if err == nil && localIP != nil {
		// 创建ECS选项
		ecs := &dns.EDNS0_SUBNET{
			Code:          dns.EDNS0SUBNET,
			Family:        1, // IPv4
			SourceNetmask: 24,
			SourceScope:   0,
			Address:       localIP,
		}

		// 创建OPT记录并添加ECS选项
		opt := &dns.OPT{
			Hdr: dns.RR_Header{
				Name:   ".",
				Rrtype: dns.TypeOPT,
			},
			Option: []dns.EDNS0{ecs},
		}

		// 将OPT记录添加到DNS查询的额外记录部分
		msg.Extra = append(msg.Extra, opt)
	} else {
		// 如果无法获取本地IP，则只使用基本的EDNS0功能
		msg.SetEdns0(4096, false)
	}

	// 将查询打包成字节
	queryBytes, err := msg.Pack()
	if err != nil {
		return nil, fmt.Errorf("打包DNS查询失败: %w", err)
	}

	// 使用base64编码查询
	b64 := base64.RawURLEncoding.EncodeToString(queryBytes)

	// 构造QUIC服务器URL
	dnsServer := "quic://" + c.server
	if !strings.Contains(c.server, ":") {
		dnsServer = "quic://" + c.server + ":853"
	}
	// 发送DoQ查询
	resp, err := sendDoQQuery(ctx, dnsServer, b64)
	if err != nil {
		return nil, fmt.Errorf("QUIC查询失败: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应体
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取QUIC响应体失败: %w", err)
	}

	// 解析响应
	addrs, err := parseDNSResponse(respBytes)
	if err != nil {
		return nil, fmt.Errorf("解析QUIC响应失败: %w", err)
	}

	return addrs, nil
}

// --------------------------- 网络请求实现 ---------------------------

func sendDoHQuery(ctx context.Context, dnsServer, b64 string, timeout time.Duration) ([]byte, error) {
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}

	// 构建请求URL
	reqURL := dnsServer + "?dns=" + b64

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建HTTP请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/dns-message")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("DoH查询失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DoH查询失败, 状态码: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应体失败: %w", err)
	}

	return body, nil
}

func sendH3Query(ctx context.Context, dnsServer, b64 string, timeout time.Duration) ([]byte, error) {
	tlsConfig := &tls.Config{InsecureSkipVerify: true}

	if strings.HasPrefix(dnsServer, "h3://") {
		dnsServer = "https://" + dnsServer[5:]
	}

	// HTTP/3 客户端
	http3Transport := &http3.Transport{
		TLSClientConfig: tlsConfig,
	}
	client := &http.Client{
		Timeout:   timeout,
		Transport: http3Transport,
	}

	// 构建请求 URL
	reqURL := dnsServer + "?dns=" + b64
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/dns-message")

	// 尝试发送 HTTP/3 请求
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("DoH3查询失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DoH3查询失败, 状态码: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应体失败: %w", err)
	}

	return body, nil
}

func sendCryptQuery(ctx context.Context, dnsServer, b64 string) ([]net.IPAddr, error) {
	queryBytes, err := base64.RawURLEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("base64解码查询失败: %w", err)
	}

	client := &dnscrypt.Client{
		Net: "udp",
	}

	stamp, err := dnsstamps.NewServerStampFromString(dnsServer)
	if err != nil {
		return nil, fmt.Errorf("解析DNS stamp失败: %w", err)
	}

	// 尝试UDP连接
	resolver, err := client.Dial(stamp.String())
	if err != nil {
		// 如果UDP失败，尝试TCP
		client.Net = "tcp"
		resolver, err = client.Dial(stamp.String())
		if err != nil {
			return nil, fmt.Errorf("连接DNSCrypt服务器失败: %w", err)
		}
	}

	msg := &dns.Msg{}
	if err := msg.Unpack(queryBytes); err != nil {
		return nil, fmt.Errorf("解包DNS查询失败: %w", err)
	}

	// 由于dnscrypt库可能不直接支持context，我们依赖client.Timeout设置
	client.Timeout = 10 * time.Second
	respMsg, err := client.Exchange(msg, resolver)
	if err != nil {
		return nil, fmt.Errorf("DNSCrypt查询失败: %w", err)
	}

	packedResp, err := respMsg.Pack()
	if err != nil {
		return nil, fmt.Errorf("打包DNS响应失败: %w", err)
	}

	return parseDNSResponse(packedResp)
}

// --------------------------- UDP/TCP 通用查询 ---------------------------

func executeDNSQuery(ctx context.Context, dnsServer string, query []byte, timeout time.Duration) ([]byte, error) {
	// 解析服务器地址和协议
	host := dnsServer
	network := "udp"

	if strings.HasPrefix(host, "udp://") {
		host = strings.TrimPrefix(host, "udp://")
		network = "udp"
	} else if strings.HasPrefix(host, "tcp://") {
		host = strings.TrimPrefix(host, "tcp://")
		network = "tcp"
	}

	// 添加默认端口
	if !strings.Contains(host, ":") {
		host += ":53" // UDP/TCP默认端口是53
	}

	// 创建连接
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, network, host)
	if err != nil {
		return nil, fmt.Errorf("连接DNS服务器失败: %w", err)
	}
	defer conn.Close()

	// 发送查询
	if network == "tcp" {
		// TCP查询需要发送2字节的长度字段
		lengthField := make([]byte, 2)
		lengthField[0] = byte(len(query) >> 8)
		lengthField[1] = byte(len(query) & 0xff)

		if _, err := conn.Write(lengthField); err != nil {
			return nil, fmt.Errorf("发送长度字段失败: %w", err)
		}
	}

	if _, err := conn.Write(query); err != nil {
		return nil, fmt.Errorf("发送查询失败: %w", err)
	}

	// 读取响应
	resp := make([]byte, 4096)

	// 设置读取超时
	deadline, ok := ctx.Deadline()
	if ok {
		conn.SetReadDeadline(deadline)
	}

	n, err := conn.Read(resp)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	// 如果是TCP连接，需要读取长度字段并确保读取完整响应
	if network == "tcp" {
		// TCP查询需要读取长度字段
		if n < 2 {
			return nil, fmt.Errorf("响应太短，无法读取长度字段")
		}

		// 读取长度字段
		respLen := int(resp[0])<<8 | int(resp[1])
		if respLen > len(resp)-2 {
			return nil, fmt.Errorf("响应长度超过缓冲区大小")
		}

		// 如果还没有读取完整响应，继续读取
		for n-2 < respLen {
			nn, err := conn.Read(resp[n:])
			if err != nil {
				return nil, fmt.Errorf("读取响应失败: %w", err)
			}
			n += nn
		}

		return resp[2 : 2+respLen], nil
	}

	return resp[:n], nil
}

// sendDoQQuery 通过 QUIC DoQ 发送 DNS 查询，并返回 HTTP 响应
func sendDoQQuery(ctx context.Context, dnsServer, b64Query string) (*http.Response, error) {
	// 规范化 DNS 服务器地址
	dnsServerAddr, err := normalizeDoQServer(dnsServer)
	if err != nil {
		return nil, fmt.Errorf("invalid DNS server: %w", err)
	}

	// 解码 Base64 查询
	queryData, err := base64.RawURLEncoding.DecodeString(b64Query)
	if err != nil {
		return nil, fmt.Errorf("decode base64 query failed: %v", err)
	}

	// 解析为 dns.Msg
	msg := new(dns.Msg)
	if err := msg.Unpack(queryData); err != nil {
		return nil, fmt.Errorf("unpack DNS query failed: %v", err)
	}

	// DoQ 要求 Message ID 为 0
	msg.Id = 0
	packedQuery, err := msg.Pack()
	if err != nil {
		return nil, fmt.Errorf("pack DNS query failed: %v", err)
	}

	// 配置 TLS
	tlsConf := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"doq"},
	}

	// 建立 QUIC 连接
	conn, err := quic.DialAddr(ctx, dnsServerAddr, tlsConf, &quic.Config{DisablePathMTUDiscovery: true})
	if err != nil {
		return nil, fmt.Errorf("quic dial failed: %v", err)
	}
	defer conn.CloseWithError(0, "done")

	// 打开 QUIC 流
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, fmt.Errorf("open stream failed: %v", err)
	}
	defer stream.Close()

	// 发送 DNS 查询，带 2 字节长度前缀
	if _, err := stream.Write(addPrefix(packedQuery)); err != nil {
		return nil, fmt.Errorf("write stream failed: %v", err)
	}

	// 关闭流表示发送完毕
	if err := stream.Close(); err != nil {
		return nil, fmt.Errorf("close stream failed: %v", err)
	}

	// 读取响应
	respBuf, err := io.ReadAll(stream)
	if err != nil {
		return nil, fmt.Errorf("read response failed: %v", err)
	}
	if len(respBuf) < 2 {
		return nil, fmt.Errorf("response too short")
	}

	// 解包 DNS 响应
	respMsg := new(dns.Msg)
	if err := respMsg.Unpack(respBuf[2:]); err != nil {
		return nil, fmt.Errorf("unpack response failed: %v", err)
	}

	// 打包响应返回 HTTP
	packedResp, err := respMsg.Pack()
	if err != nil {
		return nil, fmt.Errorf("pack response failed: %v", err)
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(packedResp)),
	}, nil
}

// addPrefix 为 DNS 消息加 2 字节长度前缀
func addPrefix(b []byte) []byte {
	m := make([]byte, 2+len(b))
	binary.BigEndian.PutUint16(m, uint16(len(b)))
	copy(m[2:], b)
	return m
}
func normalizeDoQServer(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}

	host := u.Host
	if host == "" {
		// 如果直接传 host:port 而不是 URL
		host = raw
	}
	if !strings.Contains(host, ":") {
		host += ":853"
	}
	return host, nil
}
