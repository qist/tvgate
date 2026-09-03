package web

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/qist/tvgate/monitor"
)

// registerV1Routes 注册 /web/api/v1/* 规范化 JSON API（SPA 专用，全走 cookieAuth）
func (h *ConfigHandler) registerV1Routes(webPath string, mux *http.ServeMux) {
	mux.HandleFunc(webPath+"api/v1/status", h.cookieAuth(h.handleV1Status))
}

// handleV1Status 系统状态聚合（CPU/内存/磁盘/负载/网络/运行时长/活跃连接），SPA 唯一状态源
func (h *ConfigHandler) handleV1Status(w http.ResponseWriter, r *http.Request) {
	sd := monitor.GetStatusData(r)
	ts := sd.TrafficStats
	resp := map[string]interface{}{
		"version":           sd.Version,
		"os":                ts.HostInfo.OS + "/" + ts.HostInfo.KernelArch,
		"uptime":            int64(sd.Uptime.Seconds()),
		"cpu":               round1(ts.CPUUsage),
		"cpu_temperature":   round1(ts.CPUTemperature),
		"cpu_count":         ts.CPUCount,
		"mem":               round1(pct(ts.MemoryUsage, ts.MemoryTotal)),
		"mem_used":          ts.MemoryUsage,
		"mem_total":         ts.MemoryTotal,
		"swap":              round1(pct(ts.SwapUsage, ts.SwapTotal)),
		"disk":              round1(ts.DiskUsedPercent),
		"disk_used":         ts.DiskUsage,
		"disk_total":        ts.DiskTotal,
		"load":              map[string]float64{"load1": ts.LoadAverage.Load1, "load5": ts.LoadAverage.Load5, "load15": ts.LoadAverage.Load15},
		"clients":           len(sd.ActiveClients),
		"connections":       ts.ActiveConnections,
		"total_connections": ts.TotalConnections,
		"in_bytes":          ts.InboundBytes,
		"out_bytes":         ts.OutboundBytes,
		"in_bandwidth":      ts.InboundBandwidth,
		"out_bandwidth":     ts.OutboundBandwidth,
		"interfaces":        ts.NetworkInterfaces,
		"proxy_groups":      len(sd.ProxyGroups),
		"goroutines":        sd.Goroutines,
		"web_path":          sd.WebPath,
		"timestamp":         sd.Timestamp.Format(time.RFC3339),
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(resp)
}

// round1 保留 1 位小数
func round1(v float64) float64 { return float64(int(v*10+0.5)) / 10 }

// pct 百分比
func pct(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(used) * 100 / float64(total)
}
