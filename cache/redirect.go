package cache
import (
	"time"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
)
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

	// 添加新IP
	newLevel := currentLevel + 1
	chain[newLevel] = redirectIP
	chainData.ChainHead = newLevel
	seen[redirectIP] = struct{}{}
	logger.LogPrintf("追加重定向链: %s L%d -> %s", originalDomain, newLevel, redirectIP)

	// 链接 redirectIP 自己的链
	if nextChainData, found := config.RedirectCache.Mapping[redirectIP]; found {
		nextChain := nextChainData.Chain
		for _, nextIP := range nextChain {
			if _, exists := seen[nextIP]; !exists {
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