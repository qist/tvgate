package rules
import (
	"net"
	"strings"
	"github.com/qist/tvgate/config"
)
func FallbackMatch(host string, ip net.IP, group *config.ProxyGroupConfig) bool {
	// logPrintf("回退原始匹配 - 主机: %s, IP: %v", host, ip)

	for _, pattern := range group.Domains {
		if strings.Contains(pattern, "/") && ip != nil {
			if isIPMatch(ip.String(), pattern) {
				// logPrintf("原始IP匹配 - IP: %s, 规则: %s (代理组: %s)",
				// ip.String(), pattern, getGroupName(group))
				return true
			}
			continue
		}

		if !strings.Contains(pattern, "/") {
			if host == pattern || strings.HasSuffix(host, "."+pattern) {
				// logPrintf("原始域名匹配 - 主机: %s, 规则: %s (代理组: %s)",
				// host, pattern, getGroupName(group))
				return true
			}
		}
	}

	// logPrintf("无匹配 - 主机: %s, 代理组: %s, 规则: %v",
	// host, getGroupName(group), group.Domains)
	return false
}