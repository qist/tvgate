package dns

import (
	"context"
	"net"
	"time"
)

// NetResolver 包装net.Resolver以使用自定义DNS解析器
type NetResolver struct {
	resolver *Resolver
}

// NewNetResolver 创建一个新的NetResolver实例
func NewNetResolver() *NetResolver {
	return &NetResolver{
		resolver: GetInstance(),
	}
}

// LookupIPAddr 实现net.Resolver接口
func (nr *NetResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return nr.resolver.LookupIPAddr(ctx, host)
}

// LookupIP 实现IP解析
func (nr *NetResolver) LookupIP(ctx context.Context, network, host string) ([]net.IP, error) {
	addrs, err := nr.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	
	ips := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		ips = append(ips, addr.IP)
	}
	
	return ips, nil
}

// Dialer 包装net.Dialer以使用自定义DNS解析器
type Dialer struct {
	*net.Dialer
	resolver *Resolver
}

// NewDialer 创建一个新的Dialer实例
func NewDialer() *Dialer {
	resolver := GetInstance()
	
	return &Dialer{
		Dialer: &net.Dialer{
			Timeout:   resolver.timeout,
			KeepAlive: 30 * time.Second,
			Resolver:  resolver.GetNetResolver(),
		},
		resolver: resolver,
	}
}

// NewFallbackDialer 创建一个使用系统DNS的回退Dialer实例
func NewFallbackDialer() *Dialer {
	resolver := GetInstance()
	
	return &Dialer{
		Dialer: &net.Dialer{
			Timeout:   resolver.timeout,
			KeepAlive: 30 * time.Second,
			Resolver:  resolver.GetSystemResolver(),
		},
		resolver: resolver,
	}
}