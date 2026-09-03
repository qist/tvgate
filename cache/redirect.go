package cache

import (
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
)

// maxChainLen 单条重定向链的最大长度：IPTV CDN 会长期轮换 IP，
// 活跃域名（TTL 清理器不会回收）的链必须有上限，否则无限膨胀。
const maxChainLen = 32

// 添加重定向IP到缓存 (支持链式)
func AddRedirectIP(originalDomain string, redirectIP string) {
	if originalDomain == "" || redirectIP == "" {
		return
	}
	config.RedirectCache.Lock()
	defer config.RedirectCache.Unlock()

	// 初始化 redirectChain（如果不存在）
	if _, exists := config.RedirectCache.Mapping[originalDomain]; !exists {
		config.RedirectCache.Mapping[originalDomain] = &config.RedirectChain{
			Chain:     make(map[int]string),
			ChainHead: 1,
			LastUsed:  time.Now(),
		}
		config.RedirectCache.Mapping[originalDomain].Chain[1] = redirectIP
		logger.LogPrintf("记录新重定向链: %s -> %s", originalDomain, redirectIP)
		return
	}

	chainData := config.RedirectCache.Mapping[originalDomain]
	chain := chainData.Chain
	currentLevel := chainData.ChainHead
	chainData.LastUsed = time.Now()

	// 构建当前链的去重集合
	seen := make(map[string]struct{})
	for _, ip := range chain {
		seen[ip] = struct{}{}
	}

	// 检查是否已存在
	if _, exists := seen[redirectIP]; exists {
		return
	}

	// 添加新IP（链长超限时丢弃最旧记录并压缩 level，保持 1..ChainHead 连续）
	newLevel := currentLevel + 1
	if newLevel > maxChainLen {
		compacted := make(map[int]string, maxChainLen)
		lvl := 1
		for i := currentLevel - maxChainLen + 2; i <= currentLevel; i++ {
			if ip, ok := chain[i]; ok {
				compacted[lvl] = ip
				lvl++
			}
		}
		chainData.Chain = compacted
		chain = compacted
		currentLevel = lvl - 1
		newLevel = lvl
	}
	chain[newLevel] = redirectIP
	chainData.ChainHead = newLevel
	seen[redirectIP] = struct{}{}
	logger.LogPrintf("追加重定向链: %s L%d -> %s", originalDomain, newLevel, redirectIP)

	// 链接 redirectIP 自己的链（并入其链上尚未出现过的新 IP，仍受 maxChainLen 约束）
	if nextChainData, found := config.RedirectCache.Mapping[redirectIP]; found {
		nextChain := nextChainData.Chain
		for _, nextIP := range nextChain {
			if newLevel >= maxChainLen {
				break
			}
			if _, exists := seen[nextIP]; exists {
				continue // 跳过重复 IP
			}
			newLevel++
			chain[newLevel] = nextIP
			seen[nextIP] = struct{}{}
		}
		chainData.ChainHead = newLevel
		logger.LogPrintf("链接重定向链: %s + %s", originalDomain, redirectIP)
	}
}
