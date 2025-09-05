package monitor

import (
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/disk"
	"github.com/shirou/gopsutil/host"
	"github.com/shirou/gopsutil/load"
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

// 系统负载信息
type LoadAverageInfo struct {
	Load1  float64
	Load5  float64
	Load15 float64
}

// 主机信息
type HostInfo struct {
	OS          string
	Platform    string
	KernelArch  string
	KernelVersion string
}

// 网络接口信息
type NetworkInterfaceInfo struct {
	Name        string
	BytesRecv   uint64
	BytesSent   uint64
	PacketsRecv uint64
	PacketsSent uint64
	RecvBandwidth uint64 // 实时接收带宽 (bytes/sec)
	SendBandwidth uint64 // 实时发送带宽 (bytes/sec)
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
	InboundBandwidth  uint64 // 实时入流量带宽
	OutboundBandwidth uint64 // 实时出流量带宽

	CPUUsage        float64
	CPUCount        int
	MemoryUsage     uint64
	MemoryTotal     uint64
	DiskUsage       uint64
	DiskTotal       uint64
	DiskUsedPercent float64
	DiskPartitions  []DiskPartitionInfo // 添加所有分区信息

	LoadAverage LoadAverageInfo
	HostInfo    HostInfo

	NetworkInterfaces []NetworkInterfaceInfo // 网络接口信息

	ProxyGroupStats map[string]*ProxyGroupTraffic
	LastUpdate      time.Time
	PrevNetCounters map[string]net.IOCountersStat // 用于计算带宽的上一次计数器
	mu              sync.RWMutex
}

// 全局流量统计实例
var GlobalTrafficStats = &TrafficStats{
	ProxyGroupStats: make(map[string]*ProxyGroupTraffic),
	LastUpdate:      time.Now(),
	PrevNetCounters: make(map[string]net.IOCountersStat),
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
	
	// 深拷贝 NetworkInterfaces
	networkInterfacesCopy := append([]NetworkInterfaceInfo(nil), ts.NetworkInterfaces...)

	return &TrafficStats{
		TotalConnections:  ts.TotalConnections,
		ActiveConnections: ts.ActiveConnections,
		TotalBytes:        ts.TotalBytes,
		InboundBytes:      ts.InboundBytes,
		OutboundBytes:     ts.OutboundBytes,
		InboundBandwidth:  ts.InboundBandwidth,
		OutboundBandwidth: ts.OutboundBandwidth,
		CPUUsage:          ts.CPUUsage,
		CPUCount:          ts.CPUCount,
		MemoryUsage:       ts.MemoryUsage,
		MemoryTotal:       ts.MemoryTotal,
		DiskUsage:         ts.DiskUsage,
		DiskTotal:         ts.DiskTotal,
		DiskUsedPercent:   ts.DiskUsedPercent,
		DiskPartitions:    partitionsCopy,   // ✅ 复制分区
		LoadAverage:       ts.LoadAverage,
		HostInfo:          ts.HostInfo,
		NetworkInterfaces: networkInterfacesCopy, // ✅ 复制网络接口信息
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
	// 使用500毫秒间隔采样以提高准确性
	cpuPercent, _ := cpu.Percent(500*time.Millisecond, false)
	cpuUsage := 0.0
	if len(cpuPercent) > 0 {
		cpuUsage = cpuPercent[0]
	}
	
	// CPU 核心数
	cpuCount, _ := cpu.Counts(true)
	
	// 修正CPU使用率计算方式：总使用率除以核心数得到平均使用率
	if cpuCount > 0 {
		cpuUsage = cpuUsage / float64(cpuCount)
	}
	
	// 确保CPU使用率在合理范围内 (0-100%)
	if cpuUsage < 0.0 {
		cpuUsage = 0.0
	}
	if cpuUsage > 100.0 {
		cpuUsage = 100.0
	}
	
	// 内存使用
	vmem, _ := mem.VirtualMemory()
	memUsage := uint64(0)
	memTotal := uint64(0)
	if vmem != nil {
		memUsage = vmem.Used
		memTotal = vmem.Total
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

		// 记录磁盘总量，只取第一个"主要分区"作为整体磁盘使用率
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
	
	// 系统负载信息
	loadAvg, _ := load.Avg()
	loadAverage := LoadAverageInfo{}
	if loadAvg != nil {
		loadAverage.Load1 = loadAvg.Load1
		loadAverage.Load5 = loadAvg.Load5
		loadAverage.Load15 = loadAvg.Load15
	}
	
	// 主机信息
	hostInfo, _ := host.Info()
	hostDetails := HostInfo{}
	if hostInfo != nil {
		hostDetails.OS = hostInfo.OS
		hostDetails.Platform = hostInfo.Platform
		hostDetails.KernelArch = hostInfo.KernelArch
		hostDetails.KernelVersion = hostInfo.KernelVersion
	}

	// 网络流量
	counters, _ := net.IOCounters(true)
	var totalIn, totalOut uint64
	for _, c := range counters {
		totalIn += c.BytesRecv
		totalOut += c.BytesSent
	}
	
	// 计算实时总带宽
	var inboundBandwidth, outboundBandwidth uint64
	now := time.Now()
	GlobalTrafficStats.mu.Lock()
	// 保存旧的总流量值用于带宽计算
	oldTotalIn := GlobalTrafficStats.InboundBytes
	oldTotalOut := GlobalTrafficStats.OutboundBytes
	timeDiff := now.Sub(GlobalTrafficStats.LastUpdate).Seconds()
	
	if timeDiff > 0 {
		inboundDiff := totalIn - oldTotalIn
		outboundDiff := totalOut - oldTotalOut
		
		inboundBandwidth = uint64(float64(inboundDiff) / timeDiff)
		outboundBandwidth = uint64(float64(outboundDiff) / timeDiff)
	}
	
	// 网络接口信息和带宽计算
	networkInterfaces := make([]NetworkInterfaceInfo, 0)
	
	for _, c := range counters {
		interfaceInfo := NetworkInterfaceInfo{
			Name:        c.Name,
			BytesRecv:   c.BytesRecv,
			BytesSent:   c.BytesSent,
			PacketsRecv: c.PacketsRecv,
			PacketsSent: c.PacketsSent,
		}
		
		// 计算实时带宽
		if timeDiff > 0 {
			if prev, exists := GlobalTrafficStats.PrevNetCounters[c.Name]; exists {
				recvDiff := c.BytesRecv - prev.BytesRecv
				sendDiff := c.BytesSent - prev.BytesSent
				
				interfaceInfo.RecvBandwidth = uint64(float64(recvDiff) / timeDiff)
				interfaceInfo.SendBandwidth = uint64(float64(sendDiff) / timeDiff)
			}
		}
		
		networkInterfaces = append(networkInterfaces, interfaceInfo)
	}
	
	// 更新上一次的计数器
	prevCounters := make(map[string]net.IOCountersStat)
	for _, c := range counters {
		prevCounters[c.Name] = c
	}
	GlobalTrafficStats.mu.Unlock()

	// 更新全局流量统计
	GlobalTrafficStats.mu.Lock()
	defer GlobalTrafficStats.mu.Unlock()
	GlobalTrafficStats.CPUUsage = cpuUsage
	GlobalTrafficStats.CPUCount = cpuCount
	GlobalTrafficStats.MemoryUsage = memUsage
	GlobalTrafficStats.MemoryTotal = memTotal
	GlobalTrafficStats.DiskUsage = diskUsage
	GlobalTrafficStats.DiskTotal = diskTotal
	GlobalTrafficStats.DiskUsedPercent = diskUsedPercent
	GlobalTrafficStats.DiskPartitions = diskPartitions
	GlobalTrafficStats.LoadAverage = loadAverage
	GlobalTrafficStats.HostInfo = hostDetails
	GlobalTrafficStats.NetworkInterfaces = networkInterfaces
	GlobalTrafficStats.PrevNetCounters = prevCounters
	GlobalTrafficStats.InboundBytes = totalIn
	GlobalTrafficStats.OutboundBytes = totalOut
	GlobalTrafficStats.InboundBandwidth = inboundBandwidth
	GlobalTrafficStats.OutboundBandwidth = outboundBandwidth
	GlobalTrafficStats.TotalBytes = totalIn + totalOut
	GlobalTrafficStats.LastUpdate = time.Now()
}