package dns

import (
	"context"
	"fmt"
	"net"
)

// LookupIPWithIPv6Disabled 使用默认解析器解析IP地址，但只返回IPv4地址
func LookupIPWithIPv6Disabled(ctx context.Context, host string) ([]net.IPAddr, error) {
	ips, err := GetInstance().LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	
	// 过滤IPv4地址
	var ipv4Addrs []net.IPAddr
	for _, ip := range ips {
		if ip.IP.To4() != nil {
			ipv4Addrs = append(ipv4Addrs, ip)
		}
	}
	
	if len(ipv4Addrs) == 0 {
		return nil, fmt.Errorf("no IPv4 addresses found for %s", host)
	}
	
	return ipv4Addrs, nil
}

// LookupIP 使用默认解析器解析IP地址
func LookupIP(host string) ([]net.IP, error) {
	return GetInstance().LookupIP(host)
}

// LookupIPAddr 使用默认解析器解析IP地址（返回net.IPAddr数组）
func LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return GetInstance().LookupIPAddr(ctx, host)
}

// RefreshConfig 刷新DNS配置
func RefreshConfig() {
	GetInstance().RefreshConfig()
}

// GetResolvers 获取当前使用的DNS服务器列表
func GetResolvers() []string {
	return GetInstance().GetResolvers()
}

// GetDialer 获取使用自定义DNS的拨号器
func GetDialer() *net.Dialer {
	return GetInstance().GetDialer()
}

// GetFallbackDialer 获取使用系统DNS的回退拨号器
func GetFallbackDialer() *net.Dialer {
	return GetInstance().GetFallbackDialer()
}

// GetNetResolver 获取可以用于net包的解析器
func GetNetResolver() *net.Resolver {
	return GetInstance().GetNetResolver()
}

// GetSystemResolver 获取系统解析器
func GetSystemResolver() *net.Resolver {
	return GetInstance().GetSystemResolver()
}