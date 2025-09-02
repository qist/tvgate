package rules

import (
	"github.com/qist/tvgate/config"
)

// 查找完整重定向链
func FindFullChain(node string) []string {
	config.RedirectCache.RLock()
	defer config.RedirectCache.RUnlock()

	// 1. 找到链头原始域名（键）
	var originalDomain string
	for domain := range config.RedirectCache.Mapping {
		visited := make(map[string]struct{})
		if dfsContains(domain, node, visited) {
			originalDomain = domain
			break
		}
	}
	if originalDomain == "" {
		// 没找到，直接返回传入节点本身
		return []string{node}
	}

	// 2. 根据链头，取完整链
	chain := []string{originalDomain}
	chainData := config.RedirectCache.Mapping[originalDomain]
	for i := 1; i <= chainData.ChainHead; i++ {
		if ip, ok := chainData.Chain[i]; ok {
			chain = append(chain, ip)
		}
	}

	return chain
}

// 辅助递归函数：检查链头链条是否包含目标节点
func dfsContains(current, target string, visited map[string]struct{}) bool {
	if current == target {
		return true
	}
	if _, ok := visited[current]; ok {
		return false
	}
	visited[current] = struct{}{}

	chainData, exists := config.RedirectCache.Mapping[current]
	if !exists {
		return false
	}

	for i := 1; i <= chainData.ChainHead; i++ {
		next, ok := chainData.Chain[i]
		if !ok {
			continue
		}
		if dfsContains(next, target, visited) {
			return true
		}
	}

	return false
}
