package monitor

import (
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/disk"
	"github.com/shirou/gopsutil/mem"
	"github.com/shirou/gopsutil/net"
	"runtime"
	"strings"
	// "fmt"
	"sync"
	"time"
)

// 磁盘分区信息
type DiskPartitionInfo struct {
	Path        string
	Total       uint64
	Used        uint64
	Free        uint64
	UsedPercent float64
	FsType      string
	MountPoint  string
}

// ProxyGroupTraffic 代理组流量统计
type ProxyGroupTraffic struct {
	GroupName        string
	Connections      int64
	BytesTransferred int64
	ActiveStreams    int64
	LastError        string
	LastActivity     time.Time
}

// TrafficStats 系统与流量统计
type TrafficStats struct {
	TotalConnections  int64
	ActiveConnections int64
	TotalBytes        uint64

	InboundBytes  uint64 // 入口流量
	OutboundBytes uint64 // 出口流量

	CPUUsage        float64
	MemoryUsage     uint64
	DiskUsage       uint64
	DiskTotal       uint64
	DiskUsedPercent float64
	DiskPartitions  []DiskPartitionInfo // 添加所有分区信息

	ProxyGroupStats map[string]*ProxyGroupTraffic
	LastUpdate      time.Time
	mu              sync.RWMutex
}

// 全局流量统计实例
var GlobalTrafficStats = &TrafficStats{
	ProxyGroupStats: make(map[string]*ProxyGroupTraffic),
	LastUpdate:      time.Now(),
}

func (ts *TrafficStats) GetTrafficStats() *TrafficStats {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	// 深拷贝 ProxyGroupStats
	proxyStatsCopy := make(map[string]*ProxyGroupTraffic)
	for name, g := range ts.ProxyGroupStats {
		proxyStatsCopy[name] = &ProxyGroupTraffic{
			GroupName:        g.GroupName,
			Connections:      g.Connections,
			BytesTransferred: g.BytesTransferred,
			ActiveStreams:    g.ActiveStreams,
			LastError:        g.LastError,
			LastActivity:     g.LastActivity,
		}
	}

	// 深拷贝 DiskPartitions
	partitionsCopy := append([]DiskPartitionInfo(nil), ts.DiskPartitions...)

	return &TrafficStats{
		TotalConnections:  ts.TotalConnections,
		ActiveConnections: ts.ActiveConnections,
		TotalBytes:        ts.TotalBytes,
		InboundBytes:      ts.InboundBytes,
		OutboundBytes:     ts.OutboundBytes,
		CPUUsage:          ts.CPUUsage,
		MemoryUsage:       ts.MemoryUsage,
		DiskUsage:         ts.DiskUsage,
		DiskTotal:         ts.DiskTotal,
		DiskUsedPercent:   ts.DiskUsedPercent,
		DiskPartitions:    partitionsCopy,   // ✅ 复制分区
		ProxyGroupStats:   proxyStatsCopy,   // ✅ 复制代理组
		LastUpdate:        ts.LastUpdate,
	}
}


// StartSystemStatsUpdater 定时更新系统资源
func StartSystemStatsUpdater(interval time.Duration) {
	go func() {
		for {
			updateSystemStats()
			time.Sleep(interval)
		}
	}()
}

func updateSystemStats() {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	// CPU 使用率
	cpuPercent, _ := cpu.Percent(0, false)
	cpuUsage := 0.0
	if len(cpuPercent) > 0 {
		cpuUsage = cpuPercent[0]
	}

	// 内存使用
	vmem, _ := mem.VirtualMemory()
	memUsage := uint64(0)
	if vmem != nil {
		memUsage = vmem.Used
	}

	// 磁盘分区列表
	diskPartitions := make([]DiskPartitionInfo, 0)
	parts, _ := disk.Partitions(true)

	var diskUsage, diskTotal uint64
	var diskUsedPercent float64

	for _, part := range parts {
		// fmt.Printf("[DEBUG] Device=%s Mountpoint=%s Fstype=%s\n", part.Device, part.Mountpoint, part.Fstype)

		// 仅在 Linux 上过滤虚拟分区
		if runtime.GOOS != "windows" {
			if strings.HasPrefix(part.Mountpoint, "/proc") ||
				strings.HasPrefix(part.Mountpoint, "/sys") ||
				strings.HasPrefix(part.Mountpoint, "/run") ||
				strings.HasPrefix(part.Mountpoint, "/dev") ||
				part.Fstype == "tmpfs" || part.Fstype == "devtmpfs" {
				// fmt.Println("   -> skipped (virtual fs)")
				continue
			}
		}

		stat, err := disk.Usage(part.Mountpoint)
		if err != nil {
			// fmt.Printf("   -> Usage error: %v\n", err)
			continue
		}

		// fmt.Printf("   -> Path=%s Total=%d Used=%d Free=%d UsedPercent=%.2f%%\n",
		// 	stat.Path, stat.Total, stat.Used, stat.Free, stat.UsedPercent)

		// 避免重复分区
		skip := false
		for _, p := range diskPartitions {
			if p.MountPoint == stat.Path {
				skip = true
				break
			}
		}
		if skip {
			// fmt.Println("   -> skipped (duplicate)")
			continue
		}

		// 记录磁盘总量，只取第一个“主要分区”作为整体磁盘使用率
		if diskTotal == 0 {
			diskUsage = stat.Used
			diskTotal = stat.Total
			diskUsedPercent = stat.UsedPercent
		}

		diskPartitions = append(diskPartitions, DiskPartitionInfo{
			Path:        part.Device,
			Total:       stat.Total,
			Used:        stat.Used,
			Free:        stat.Free,
			UsedPercent: stat.UsedPercent,
			FsType:      part.Fstype,
			MountPoint:  stat.Path,
		})
	}

	// 网络流量
	counters, _ := net.IOCounters(true)
	var totalIn, totalOut uint64
	for _, c := range counters {
		totalIn += c.BytesRecv
		totalOut += c.BytesSent
	}

	// 更新全局流量统计
	GlobalTrafficStats.mu.Lock()
	defer GlobalTrafficStats.mu.Unlock()
	GlobalTrafficStats.CPUUsage = cpuUsage
	GlobalTrafficStats.MemoryUsage = memUsage
	GlobalTrafficStats.DiskUsage = diskUsage
	GlobalTrafficStats.DiskTotal = diskTotal
	GlobalTrafficStats.DiskUsedPercent = diskUsedPercent
	GlobalTrafficStats.DiskPartitions = diskPartitions
	GlobalTrafficStats.InboundBytes = totalIn
	GlobalTrafficStats.OutboundBytes = totalOut
	GlobalTrafficStats.TotalBytes = totalIn + totalOut
	GlobalTrafficStats.LastUpdate = time.Now()
}
