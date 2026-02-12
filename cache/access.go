package cache

import (
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/utils/proxy"
	tsync "github.com/qist/tvgate/utils/sync"
)

var accessWg tsync.WaitGroup

func LoadAccessCache(key string) *config.ProxyGroupConfig {
	config.AccessCache.RLock()
	cached, ok := config.AccessCache.Mapping[key]
	config.AccessCache.RUnlock()
	if !ok {
		return nil
	}

	// 异步更新访问时间，防止阻塞读取
	accessWg.Go(func() {
		config.AccessCache.Lock()
		if c, exists := config.AccessCache.Mapping[key]; exists {
			c.LastUsed = time.Now()
		}
		config.AccessCache.Unlock()
	})

	return cached.Group
}

func StoreAccessCache(key string, group *config.ProxyGroupConfig) {
	if group == nil {
		logger.LogPrintf("跳过存入无效访问缓存: %q", key)
		return
	}
	config.AccessCache.Lock()
	defer config.AccessCache.Unlock()
	config.AccessCache.Mapping[key] = &config.CachedGroup{
		Group:    group,
		LastUsed: time.Now(),
	}
	logger.LogPrintf("存入访问缓存: %s -> %s", key, proxy.GetGroupName(group))
}
