package rules

import (
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/config"
	"net"
	"path/filepath"
	"strings"
)

// 获取原始域名或 IP
func ExtractOriginalDomain(path string) string {
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if part == "" {
			continue
		}
		if ip := net.ParseIP(part); ip != nil {
			for _, group := range config.Cfg.ProxyGroups {
				for _, pattern := range group.Domains {
					if strings.Contains(pattern, "/") && isIPMatch(ip.String(), pattern) {
						logger.LogPrintf("IP %s 匹配代理组规则 %s", ip.String(), pattern)
						return ip.String()
					}
				}
			}
		} else if strings.Contains(part, ".") {
			for _, group := range config.Cfg.ProxyGroups {
				for _, pattern := range group.Domains {
					if !strings.Contains(pattern, "/") {
						// 通配符匹配
						if strings.Contains(pattern, "*") || strings.Contains(pattern, "?") {
							if ok, _ := filepath.Match(pattern, part); ok {
								logger.LogPrintf("域名 %s 匹配代理组规则(通配) %s", part, pattern)
								return part
							}
						}
						// 精确或后缀匹配
						if part == pattern || strings.HasSuffix(part, "."+pattern) {
							logger.LogPrintf("域名 %s 匹配代理组规则 %s", part, pattern)
							return part
						}
					}
				}
			}
		}
	}
	return ""
}
