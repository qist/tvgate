package player

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
)

// 订阅内 `proxy=` 指定的内联上游代理。多个代理按 fastest 选最快——
// 复用 lb.SelectProxy / proxy.CreateProxyClient（与 config.yaml 的 proxygroups 同一套逻辑）。
//
// inlineGroups 以「规范化后的代理列表」为键缓存代理组：让同一频道的多节点也能跨请求
// 复用测速统计（否则每次拉流/每个分片都重新并发测速，代价高且选点抖动）。
var inlineGroups sync.Map // normalized spec -> *config.ProxyGroupConfig

// inlineProxyGroup 把订阅内 proxy= 拆出的代理地址解析成代理组（命中缓存则复用）。
// 无有效代理返回 nil，调用方据此回落到域名规则代理组 / 直连。
func inlineProxyGroup(specs []string) *config.ProxyGroupConfig {
	list := parseProxySpecs(specs)
	if len(list) == 0 {
		return nil
	}
	key := proxyGroupKey(list)
	if v, ok := inlineGroups.Load(key); ok {
		return v.(*config.ProxyGroupConfig)
	}
	g := &config.ProxyGroupConfig{
		Proxies:     list,
		LoadBalance: "fastest", // 与 proxygroups 的 loadbalance: fastest 一致
		Interval:    60 * time.Second,
		MaxRetries:  1,
		RetryDelay:  time.Second,
		Stats:       &config.GroupStats{ProxyStats: make(map[string]*config.ProxyStats)},
	}
	actual, _ := inlineGroups.LoadOrStore(key, g)
	return actual.(*config.ProxyGroupConfig)
}

// parseProxySpecs 解析 proxy= 值（可含多个），逐个转成 config.ProxyConfig，去重保序。
func parseProxySpecs(specs []string) []*config.ProxyConfig {
	var out []*config.ProxyConfig
	seen := make(map[string]bool)
	for _, raw := range specs {
		for _, part := range splitProxyList(raw) {
			pc, err := parseProxyURL(part)
			if err != nil {
				logger.LogPrintf("[player] 忽略无效 proxy 配置 %q: %v", part, err)
				continue
			}
			k := proxyGroupKey([]*config.ProxyConfig{pc})
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, pc)
		}
	}
	return out
}

// parseProxyURL 解析单个代理地址。支持：
//
//	socks5://[user:pass@]host:port、socks4://、socks4a://、http://、https://
//	省略 scheme 时默认 http://（http 代理最常见）
//	省略端口时按类型取默认（socks 1080 / https 443 / http 8080）
func parseProxyURL(raw string) (*config.ProxyConfig, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, fmt.Errorf("空代理地址")
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return nil, err
	}
	typ := strings.ToLower(u.Scheme)
	switch typ {
	case "http", "https", "socks5", "socks4", "socks4a":
	default:
		return nil, fmt.Errorf("不支持的代理类型 %q", typ)
	}
	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("缺少代理服务器地址")
	}
	port := 0
	if p := u.Port(); p != "" {
		port, err = strconv.Atoi(p)
		if err != nil || port <= 0 || port > 65535 {
			return nil, fmt.Errorf("代理端口无效: %q", p)
		}
	} else {
		switch typ {
		case "https":
			port = 443
		case "socks5", "socks4", "socks4a":
			port = 1080
		default:
			port = 8080
		}
	}
	pc := &config.ProxyConfig{
		Name:   typ + "-" + host + ":" + strconv.Itoa(port),
		Type:   typ,
		Server: host,
		Port:   port,
	}
	if u.User != nil {
		pc.Username = u.User.Username()
		if pw, ok := u.User.Password(); ok {
			pc.Password = pw
		}
	}
	return pc, nil
}

// proxyGroupKey 生成代理列表的稳定缓存键（含认证信息，避免不同密码误复用）。
func proxyGroupKey(list []*config.ProxyConfig) string {
	parts := make([]string, 0, len(list))
	for _, p := range list {
		parts = append(parts, fmt.Sprintf("%s://%s:%d|%s|%s", p.Type, p.Server, p.Port, p.Username, p.Password))
	}
	return strings.Join(parts, ",")
}
