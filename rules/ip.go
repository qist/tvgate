package rules

import (
	"net"
	"strings"
)
// isIPMatch 判断IP是否匹配 CIDR 或子网规则
func isIPMatch(ipStr string, pattern string) bool {
	// 解析目标IP
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}

	// 判断IP版本匹配
	isIPv6 := strings.Contains(ipStr, ":")
	patternIsIPv6 := strings.Contains(pattern, ":")

	// 版本不匹配直接返回false
	if isIPv6 != patternIsIPv6 {
		return false
	}

	// CIDR匹配
	if strings.Contains(pattern, "/") {
		_, ipnet, err := net.ParseCIDR(pattern)
		if err != nil {
			return false
		}
		return ipnet.Contains(ip)
	}

	// 普通IP匹配
	patternIP := net.ParseIP(pattern)
	if patternIP != nil {
		return ip.Equal(patternIP)
	}

	return false
}