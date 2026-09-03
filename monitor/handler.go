package monitor

import (
	"fmt"
	"net"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/qist/tvgate/config"
)

// 页面数据结构
type StatusData struct {
	Timestamp     time.Time
	Uptime        time.Duration
	Version       string
	Goroutines    int
	MemoryStats   runtime.MemStats
	ProxyGroups   map[string]*config.ProxyGroupConfig
	TrafficStats  *TrafficStats
	ClientIP      string
	ActiveClients []*ClientConnection
	WebPath       string
}

// GetStatusData 返回状态数据快照（供 /web/api/v1/status 等 JSON API 复用）
func GetStatusData(r *http.Request) StatusData {
	return prepareStatusData(r)
}

// 字节格式化
func FormatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// 带宽格式化
func FormatBytesPerSec(bytes uint64, _ uint64) string {
	return FormatBytes(bytes) + "/s"
}

// 网络流量带宽格式化
func FormatNetworkBandwidth(bytes uint64) string {
	return FormatBytes(bytes) + "/s"
}

func prepareStatusData(r *http.Request) StatusData {
	// 内存统计
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	// 获取客户端 IP
	clientIP := GetClientIP(r)

	// 复制 ProxyGroups
	config.CfgMu.RLock()
	proxyGroups := make(map[string]*config.ProxyGroupConfig)
	for name, group := range config.Cfg.ProxyGroups {
		groupCopy := &config.ProxyGroupConfig{
			Proxies:     make([]*config.ProxyConfig, len(group.Proxies)),
			Domains:     group.Domains,
			LoadBalance: group.LoadBalance,
			Stats:       &config.GroupStats{ProxyStats: make(map[string]*config.ProxyStats)},
		}
		for i, p := range group.Proxies {
			groupCopy.Proxies[i] = &config.ProxyConfig{
				Name:   p.Name,
				Type:   p.Type,
				Server: p.Server,
				Port:   0,
				UDP:    p.UDP,
			}
			if group.Stats != nil && group.Stats.ProxyStats != nil {
				if stats, ok := group.Stats.ProxyStats[p.Name]; ok {
					groupCopy.Stats.ProxyStats[p.Name] = stats
				} else {
					groupCopy.Stats.ProxyStats[p.Name] = &config.ProxyStats{}
				}
			} else {
				groupCopy.Stats.ProxyStats[p.Name] = &config.ProxyStats{}
			}
		}
		proxyGroups[name] = groupCopy
	}
	config.CfgMu.RUnlock()

	// 获取系统与应用流量统计（深拷贝）
	trafficStats := GlobalTrafficStats.GetTrafficStats()

	return StatusData{
		Timestamp:     time.Now(),
		Uptime:        time.Since(config.StartTime),
		Version:       config.Version,
		Goroutines:    runtime.NumGoroutine(),
		MemoryStats:   memStats,
		ProxyGroups:   proxyGroups,
		TrafficStats:  trafficStats, // 包含系统统计 + 应用统计
		ClientIP:      clientIP,
		ActiveClients: ActiveClients.GetAll(),
		WebPath:       config.Cfg.Web.Path, // 注入动态 Web.Path
	}
}

func GetClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	if xr := r.Header.Get("X-Real-IP"); xr != "" {
		return xr
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}
