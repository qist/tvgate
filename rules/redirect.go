package rules

import (
	"github.com/qist/tvgate/config"
	"net"
	"net/url"
	"strings"
)

// 获取重定向链中的所有域名和IP
func GetRedirectChainHosts(targetURL string) []string {
	hosts := make([]string, 0)

	// 1. 从URL路径提取主机信息
	u, err := url.Parse(targetURL)
	if err != nil {
		return hosts
	}

	parts := strings.Split(u.Path, "/")
	for _, part := range parts {
		if part == "" {
			continue
		}

		if partURL, err := url.Parse(part); err == nil && partURL.Host != "" {
			hosts = append(hosts, partURL.Hostname())
		} else if ip := net.ParseIP(part); ip != nil {
			hosts = append(hosts, part)
		} else if strings.Contains(part, ".") {
			hosts = append(hosts, part)
		}
	}

	// 2. 获取完整重定向链
	config.RedirectCache.RLock()
	defer config.RedirectCache.RUnlock()

	seen := make(map[string]struct{})
	for _, host := range hosts {
		if chainData, exists := config.RedirectCache.Mapping[host]; exists {
			for i := 1; i <= chainData.ChainHead; i++ {
				if ip, ok := chainData.Chain[i]; ok {
					if _, added := seen[ip]; !added {
						hosts = append(hosts, ip)
						seen[ip] = struct{}{}
					}
				}
			}
		}
	}

	return unique(hosts)
}

// 数组去重
func unique(slice []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0)
	for _, item := range slice {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	return result
}
