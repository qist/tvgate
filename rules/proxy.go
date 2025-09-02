package rules

import (
	"fmt"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/utils/proxy"
	"github.com/qist/tvgate/cache"
	"net"
	"net/url"
)

// 根据 originalHost / hostname 选择代理组（合并了原有的两段查找逻辑）
func ChooseProxyGroup(hostname, originalHost string) *config.ProxyGroupConfig {
	if originalHost != "" {
		if pg := GetProxyGroup(originalHost); pg != nil {
			logger.LogPrintf("使用 original_host 参数 %s 查找代理组成功", originalHost)
			return pg
		}
	}
	if pg := GetProxyGroup(hostname); pg != nil {
		logger.LogPrintf("使用当前域名 %s 查找代理组成功", hostname)
		return pg
	}
	return nil
}

func GetProxyGroup(targetURL string) *config.ProxyGroupConfig {
	u, err := url.Parse(targetURL)
	if err != nil {
		return nil
	}

	host := u.Hostname()
	ip := net.ParseIP(host)

	// originalDomain := extractOriginalDomain(u.Path)
	// cacheKey := fmt.Sprintf("%s|%s|%s", host, originalDomain, targetURL)
	cacheKey := fmt.Sprintf("%s|%s", host, targetURL)
	// 读缓存，命中更新访问时间
	cachedGroup := cache.LoadAccessCache(cacheKey)
	if cachedGroup != nil {
		logger.LogPrintf("命中访问缓存: %s -> %s", cacheKey, proxy.GetGroupName(cachedGroup))
		return cachedGroup
	} else {
		// 如果你想缓存未匹配到的情况，可以在这里处理
		logger.LogPrintf("缓存未命中或值为空，继续匹配: %s", cacheKey)
	}

	redirectHosts := GetRedirectChainHosts(targetURL)

	for _, group := range config.Cfg.ProxyGroups {
		// 匹配重定向链
		for _, redirectHost := range redirectHosts {
			redirectIP := net.ParseIP(redirectHost)
			if matchedGroup := MatchHostWithGroup(redirectHost, redirectIP, group); matchedGroup != nil {
				cache.StoreAccessCache(cacheKey, matchedGroup)
				logger.LogPrintf("重定向链匹配成功并缓存: %s -> %s", cacheKey, proxy.GetGroupName(matchedGroup))
				return matchedGroup
			}
		}

		// 匹配当前 host
		if matchedGroup := MatchHostWithGroup(host, ip, group); matchedGroup != nil {
			cache.StoreAccessCache(cacheKey, matchedGroup)
			logger.LogPrintf("当前请求匹配成功并缓存: %s -> %s", cacheKey, proxy.GetGroupName(matchedGroup))
			return matchedGroup
		}
	}

	logger.LogPrintf("没有找到匹配的代理组，目标=%s，重定向链=%v", targetURL, redirectHosts)
	// 如果想缓存未匹配结果，避免重复计算，可以解除注释：
	// cache.StoreAccessCache(cacheKey, nil)
	return nil
}
