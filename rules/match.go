package rules
import (
	"net"
	"path/filepath"
	"strings"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
)
// matchHostWithGroup 检查域名/IP是否匹配代理组（支持链头、通配符、IP段和回退匹配）
func MatchHostWithGroup(host string, ip net.IP, group *config.ProxyGroupConfig) *config.ProxyGroupConfig {
	normalizedHost := strings.ToLower(strings.TrimSpace(host))

	// 1. 获取完整重定向链
	fullChain := FindFullChain(host)
	var chainHead string
	if len(fullChain) > 0 {
		chainHead = fullChain[0]
	} else {
		chainHead = host
	}
	normalizedChainHead := strings.ToLower(strings.TrimSpace(chainHead))
	chainHeadIP := net.ParseIP(chainHead)

	// ======= [1] 优先匹配链头 =======
	for _, pattern := range group.Domains {
		normalizedPattern := strings.ToLower(strings.TrimSpace(pattern))

		// 通配符匹配
		if strings.Contains(normalizedPattern, "*") {
			if chainHeadIP == nil {
				if ok, err := filepath.Match(normalizedPattern, normalizedChainHead); err == nil && ok {
					logger.LogPrintf("域名 %s 匹配代理组规则(通配) %s", chainHead, pattern)
					return group
				}
			} else if ip != nil {
				if ok, err := filepath.Match(normalizedPattern, normalizedHost); err == nil && ok {
					logger.LogPrintf("域名 %s 匹配代理组规则(通配) %s", host, pattern)
					return group
				}
			}
			continue
		}

		// 精确或后缀匹配
		if chainHeadIP == nil && (normalizedChainHead == normalizedPattern || strings.HasSuffix(normalizedChainHead, "."+normalizedPattern)) {
			logger.LogPrintf("域名 %s 匹配代理组规则 %s", chainHead, pattern)
			return group
		}

		// IP匹配
		if chainHeadIP != nil && strings.Contains(pattern, "/") && isIPMatch(chainHeadIP.String(), pattern) {
			logger.LogPrintf("IP %s 匹配代理组规则 %s", chainHeadIP.String(), pattern)
			return group
		}
	}

	// ======= [2] 回退匹配原始 host / ip =======
	for _, pattern := range group.Domains {
		normalizedPattern := strings.ToLower(strings.TrimSpace(pattern))

		// 通配符匹配
		if strings.Contains(normalizedPattern, "*") {
			if ok, err := filepath.Match(normalizedPattern, normalizedHost); err == nil && ok {
				logger.LogPrintf("域名 %s 匹配代理组规则(通配) %s", host, pattern)
				return group
			}
			continue
		}

		// 精确或后缀匹配
		if normalizedHost == normalizedPattern || strings.HasSuffix(normalizedHost, "."+normalizedPattern) {
			logger.LogPrintf("域名 %s 匹配代理组规则 %s", host, pattern)
			return group
		}

		// IP匹配
		if ip != nil && strings.Contains(pattern, "/") && isIPMatch(ip.String(), pattern) {
			logger.LogPrintf("IP %s 匹配代理组规则 %s", ip.String(), pattern)
			return group
		}
	}

	// ======= [3] fallback =======
	if FallbackMatch(host, ip, group) {
		logger.LogPrintf("回退匹配成功: host=%s, ip=%v", host, ip)
		return group
	}

	return nil
}