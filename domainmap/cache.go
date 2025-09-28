package domainmap

import (
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/qist/tvgate/auth"
	"github.com/qist/tvgate/stream"
)

// URLCacheItem 表示缓存的URL项
type URLCacheItem struct {
	URL       string
	ExpiresAt time.Time
}

// URLCache 管理URL缓存
type URLCache struct {
	mu    sync.RWMutex
	cache map[string]*URLCacheItem
}

// URLHub 管理URL的hub模式
type URLHub struct {
	hub *stream.StreamHubs
	url string
}

var (
	urlCache = &URLCache{
		cache: make(map[string]*URLCacheItem),
	}
	urlHubs = make(map[string]*URLHub)
	hubMu   sync.RWMutex
)

// GetOrCreateHub 获取或创建一个URL Hub
func GetOrCreateHub(url string) *URLHub {
	hubMu.Lock()
	defer hubMu.Unlock()

	if hub, exists := urlHubs[url]; exists {
		return hub
	}

	hub := &URLHub{
		hub: stream.NewStreamHubs(),
		url: url,
	}
	urlHubs[url] = hub
	return hub
}

// RemoveHub 移除一个URL Hub
func RemoveHub(url string) {
	hubMu.Lock()
	defer hubMu.Unlock()

	if hub, exists := urlHubs[url]; exists {
		hub.hub.Close()
		delete(urlHubs, url)
	}
}

// GetCachedURL 获取缓存的URL
func (uc *URLCache) GetCachedURL(key string) (string, bool) {
	// 加读锁
	uc.mu.RLock()
	defer uc.mu.RUnlock()

	item, exists := uc.cache[key]
	if !exists {
		return "", false
	}

	// 检查是否过期
	if time.Now().After(item.ExpiresAt) {
		return "", false
	}

	return item.URL, true
}

// SetCachedURL 设置缓存的URL
func (uc *URLCache) SetCachedURL(key, url string, ttl time.Duration) {
	uc.mu.Lock()
	defer uc.mu.Unlock()

	uc.cache[key] = &URLCacheItem{
		URL:       url,
		ExpiresAt: time.Now().Add(ttl),
	}
}

// RemoveExpired 清除过期的缓存项
func (uc *URLCache) RemoveExpired() {
	uc.mu.Lock()
	defer uc.mu.Unlock()

	now := time.Now()
	for key, item := range uc.cache {
		if now.After(item.ExpiresAt) {
			delete(uc.cache, key)
		}
	}
}

// StartCacheCleaner 启动缓存清理器
func StartCacheCleaner() {
	ticker := time.NewTicker(5 * time.Minute)
	go func() {
		for range ticker.C {
			urlCache.RemoveExpired()
		}
	}()
}

// ValidateURL 验证URL是否有效
func (dm *DomainMapper) ValidateURL(targetURL string) bool {
	// 创建一个带超时的HTTP客户端
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	// 发送HEAD请求验证URL
	resp, err := client.Head(targetURL)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	// 检查响应状态码
	return resp.StatusCode >= 200 && resp.StatusCode < 400
}

// GetValidURL 获取有效的URL，优先使用缓存
func (dm *DomainMapper) GetValidURL(originalURL, frontendScheme, frontendHost string, tm *auth.TokenManager, tokenParam string) (string, error) {
	// 解析原始URL
	parsedURL, err := url.Parse(originalURL)
	if err != nil {
		return "", err
	}

	// 构造缓存键
	cacheKey := fmt.Sprintf("%s_%s_%s", originalURL, frontendScheme, frontendHost)

	// 尝试从缓存获取
	if cachedURL, ok := urlCache.GetCachedURL(cacheKey); ok {
		return cachedURL, nil
	}

	// 生成新的URL
	newURL, replaced, _ := dm.replaceSpecialNestedURL(parsedURL, frontendScheme, frontendHost, tm, tokenParam)
	
	// 如果URL被替换了，验证新URL的有效性
	if replaced {
		// 验证URL是否有效
		if dm.ValidateURL(newURL) {
			// 缓存有效的URL，设置1小时过期时间
			urlCache.SetCachedURL(cacheKey, newURL, time.Hour)
			return newURL, nil
		}
		
		// 如果新URL无效，回退到原始URL
		// 但仍然缓存结果以避免重复验证
		urlCache.SetCachedURL(cacheKey, originalURL, 30*time.Minute)
		return originalURL, nil
	}

	// 如果URL没有被替换，直接返回原始URL
	urlCache.SetCachedURL(cacheKey, originalURL, time.Hour)
	return originalURL, nil
}