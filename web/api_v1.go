package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/qist/tvgate/monitor"
)

// 伪文件系统/只读镜像分区（tmpfs/squashfs 等）不显示，
// Linux 只保留系统盘与正常分区盘，安卓隐藏 /system /vendor 等碎片分区
var skipPartitionFSTypes = map[string]bool{
	"tmpfs": true, "devtmpfs": true, "proc": true, "sysfs": true,
	"cgroup": true, "cgroup2": true, "overlay": true, "squashfs": true, "erofs": true,
	"ramfs": true, "debugfs": true, "tracefs": true, "mqueue": true,
	"securityfs": true, "pstore": true, "efivarfs": true, "configfs": true,
	"autofs": true, "binfmt_misc": true, "devpts": true, "fusectl": true,
	"hugetlbfs": true, "zram": true, "sdcardfs": true, "fuse": true, "vfat+fm": true,
	"rootfs": true,
}

func skipPartition(mount, fsType string) bool {
	if skipPartitionFSTypes[fsType] {
		return true
	}
	if mount == "" {
		mount = "/"
	}
	switch {
	case mount == "/proc" || mount == "/sys" || mount == "/dev" || mount == "/run":
		return true
	case strings.HasPrefix(mount, "/system"), strings.HasPrefix(mount, "/vendor"),
		strings.HasPrefix(mount, "/product"), strings.HasPrefix(mount, "/odm"),
		strings.HasPrefix(mount, "/cache"), strings.HasPrefix(mount, "/persist"):
		return true
	}
	return false
}

// registerV1Routes 注册 /web/api/v1/* 规范化 JSON API（SPA 专用，全走 cookieAuth）
func (h *ConfigHandler) registerV1Routes(webPath string, mux *http.ServeMux) {
	mux.HandleFunc(webPath+"api/v1/status", h.cookieAuth(h.handleV1Status))
}

// handleV1Status 系统状态聚合（CPU/内存/磁盘/负载/网络/运行时长/活跃连接/分区/网卡/代理组统计）。
// SPA 唯一状态源，覆盖原独立 /status 监控页的全部能力。
func (h *ConfigHandler) handleV1Status(w http.ResponseWriter, r *http.Request) {
	sd := monitor.GetStatusData(r)
	ts := sd.TrafficStats

	// 活跃客户端完整列表
	clients := make([]map[string]interface{}, 0, len(sd.ActiveClients))
	for _, c := range sd.ActiveClients {
		clients = append(clients, map[string]interface{}{
			"id":              c.ID,
			"ip":              c.IP,
			"url":             c.URL,
			"user_agent":      c.UserAgent,
			"referer":         c.Referer,
			"connection_type": c.ConnectionType,
			"is_mobile":       c.IsMobile,
			"connected_at":    c.ConnectedAt.Format(time.RFC3339),
			"last_active":     c.LastActive.Format(time.RFC3339),
		})
	}

	// 存储分区（只留系统盘/正常分区，过滤伪文件系统与安卓碎片分区；按使用率降序）
	partitions := make([]map[string]interface{}, 0, len(ts.DiskPartitions))
	for _, p := range ts.DiskPartitions {
		mount := p.MountPoint
		if mount == "" {
			mount = p.Path
		}
		if skipPartition(mount, p.FsType) {
			continue
		}
		partitions = append(partitions, map[string]interface{}{
			"path":         p.Path,
			"total":        p.Total,
			"used":         p.Used,
			"free":         p.Free,
			"used_percent": round1(p.UsedPercent),
			"fs_type":      p.FsType,
			"mount_point":  mount,
		})
	}

	// 网卡列表
	interfaces := make([]map[string]interface{}, 0, len(ts.NetworkInterfaces))
	for _, ni := range ts.NetworkInterfaces {
		interfaces = append(interfaces, map[string]interface{}{
			"name":           ni.Name,
			"bytes_recv":     ni.BytesRecv,
			"bytes_sent":     ni.BytesSent,
			"packets_recv":   ni.PacketsRecv,
			"packets_sent":   ni.PacketsSent,
			"recv_bandwidth": ni.RecvBandwidth,
			"send_bandwidth": ni.SendBandwidth,
		})
	}

	// 代理组实时流量统计
	groups := make(map[string]map[string]interface{}, len(ts.ProxyGroupStats))
	for name, g := range ts.ProxyGroupStats {
		groups[name] = map[string]interface{}{
			"connections":       g.Connections,
			"bytes_transferred": g.BytesTransferred,
			"active_streams":    g.ActiveStreams,
			"last_error":        g.LastError,
			"last_activity":     g.LastActivity.Format(time.RFC3339),
		}
	}

	resp := map[string]interface{}{
		"version":           sd.Version,
		"os":                ts.HostInfo.OS + "/" + ts.HostInfo.KernelArch,
		"platform":          ts.HostInfo.Platform,
		"kernel_arch":       ts.HostInfo.KernelArch,
		"kernel_version":    ts.HostInfo.KernelVersion,
		"uptime":            int64(sd.Uptime.Seconds()),
		"cpu":               round1(ts.CPUUsage),
		"cpu_temperature":   round1(ts.CPUTemperature),
		"cpu_count":         ts.CPUCount,
		"mem":               round1(pct(ts.MemoryUsage, ts.MemoryTotal)),
		"mem_used":          ts.MemoryUsage,
		"mem_total":         ts.MemoryTotal,
		"swap":              round1(pct(ts.SwapUsage, ts.SwapTotal)),
		"swap_used":         ts.SwapUsage,
		"swap_total":        ts.SwapTotal,
		"disk":              round1(ts.DiskUsedPercent),
		"disk_used":         ts.DiskUsage,
		"disk_total":        ts.DiskTotal,
		"disk_partitions":   partitions,
		"load":              map[string]float64{"load1": ts.LoadAverage.Load1, "load5": ts.LoadAverage.Load5, "load15": ts.LoadAverage.Load15},
		"clients":           len(sd.ActiveClients),
		"active_clients":    clients,
		"connections":       ts.ActiveConnections,
		"total_connections": ts.TotalConnections,
		"in_bytes":          ts.InboundBytes,
		"out_bytes":         ts.OutboundBytes,
		"in_bandwidth":      ts.InboundBandwidth,
		"out_bandwidth":     ts.OutboundBandwidth,
		"interfaces":        interfaces,
		"app":               map[string]interface{}{"cpu_percent": round1(ts.App.CPUPercent), "memory_usage": ts.App.MemoryUsage, "total_bytes": ts.App.TotalBytes, "in_bytes": ts.App.InboundBytes, "out_bytes": ts.App.OutboundBytes},
		"proxy_groups":      len(sd.ProxyGroups),
		"proxy_group_stats": groups,
		"goroutines":        sd.Goroutines,
		"client_ip":         sd.ClientIP,
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
