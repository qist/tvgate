package monitor

import (
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
)

// -------------------- 数据结构 --------------------

type DiskPartitionInfo struct {
	Path        string
	Total       uint64
	Used        uint64
	Free        uint64
	UsedPercent float64
	FsType      string
	MountPoint  string
}

type LoadAverageInfo struct {
	Load1  float64
	Load5  float64
	Load15 float64
}

type HostInfo struct {
	OS            string
	Platform      string
	KernelArch    string
	KernelVersion string
}

type NetworkInterfaceInfo struct {
	Name          string
	BytesRecv     uint64
	BytesSent     uint64
	PacketsRecv   uint64
	PacketsSent   uint64
	RecvBandwidth uint64 // 实时接收带宽 (bytes/sec)
	SendBandwidth uint64 // 实时发送带宽 (bytes/sec)
}

type ProxyGroupTraffic struct {
	GroupName        string
	Connections      int64
	BytesTransferred int64
	ActiveStreams    int64
	LastError        string
	LastActivity     time.Time
}

type AppStats struct {
	CPUPercent        float64
	MemoryUsage       uint64
	TotalBytes        uint64
	InboundBytes      uint64
	OutboundBytes     uint64
	InboundBandwidth  uint64
	OutboundBandwidth uint64
	LastUpdate        time.Time
	PrevIOCounters    *process.IOCountersStat
	PrevCPUTime float64 // ← 新增，用于计算 CPU 百分比
}

type TrafficStats struct {
	// 系统
	TotalConnections  int64
	ActiveConnections int64
	TotalBytes        uint64

	InboundBytes      uint64
	OutboundBytes     uint64
	InboundBandwidth  uint64
	OutboundBandwidth uint64

	CPUUsage        float64
	CPUCount        int
	MemoryUsage     uint64
	MemoryTotal     uint64
	DiskUsage       uint64
	DiskTotal       uint64
	DiskUsedPercent float64
	DiskPartitions  []DiskPartitionInfo

	LoadAverage LoadAverageInfo
	HostInfo    HostInfo

	NetworkInterfaces []NetworkInterfaceInfo

	ProxyGroupStats map[string]*ProxyGroupTraffic

	// 应用自身流量
	App AppStats

	LastUpdate      time.Time
	PrevNetCounters map[string]net.IOCountersStat
	mu              sync.RWMutex
}

// -------------------- 全局实例 --------------------

var GlobalTrafficStats = &TrafficStats{
	ProxyGroupStats: make(map[string]*ProxyGroupTraffic),
	LastUpdate:      time.Now(),
	PrevNetCounters: make(map[string]net.IOCountersStat),
}

// -------------------- 深拷贝方法 --------------------

// AddAppInboundBytes 安全增加 App 入流量
func AddAppInboundBytes(n uint64) {
	GlobalTrafficStats.mu.Lock()
	defer GlobalTrafficStats.mu.Unlock()
	GlobalTrafficStats.App.InboundBytes += n
	GlobalTrafficStats.App.TotalBytes += n
}

// AddAppOutboundBytes 安全增加 App 出流量
func AddAppOutboundBytes(n uint64) {
	GlobalTrafficStats.mu.Lock()
	defer GlobalTrafficStats.mu.Unlock()
	GlobalTrafficStats.App.OutboundBytes += n
	GlobalTrafficStats.App.TotalBytes += n
}


func (ts *TrafficStats) GetTrafficStats() *TrafficStats {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	// ProxyGroupStats 深拷贝
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

	// DiskPartitions & NetworkInterfaces
	partitionsCopy := append([]DiskPartitionInfo(nil), ts.DiskPartitions...)
	netCopy := append([]NetworkInterfaceInfo(nil), ts.NetworkInterfaces...)

	// AppStats
	var prevIO *process.IOCountersStat
	if ts.App.PrevIOCounters != nil {
		tmp := *ts.App.PrevIOCounters
		prevIO = &tmp
	}
	appCopy := ts.App
	appCopy.PrevIOCounters = prevIO

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
		DiskPartitions:    partitionsCopy,
		LoadAverage:       ts.LoadAverage,
		HostInfo:          ts.HostInfo,
		NetworkInterfaces: netCopy,
		ProxyGroupStats:   proxyStatsCopy,
		App:               appCopy,
		LastUpdate:        ts.LastUpdate,
	}
}

// -------------------- 系统统计 --------------------

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

	// CPU
	cpuPercent, _ := cpu.Percent(500*time.Millisecond, false)
	cpuUsage := 0.0
	if len(cpuPercent) > 0 {
		cpuUsage = cpuPercent[0]
	}
	cpuCount, _ := cpu.Counts(true)
	if cpuCount > 0 {
		cpuUsage = cpuUsage / float64(cpuCount)
	}
	if cpuUsage < 0 {
		cpuUsage = 0
	}
	if cpuUsage > 100 {
		cpuUsage = 100
	}

	// 内存
	vmem, _ := mem.VirtualMemory()
	memUsage, memTotal := uint64(0), uint64(0)
	if vmem != nil {
		memUsage = vmem.Used
		memTotal = vmem.Total
	}

	// 磁盘
	diskPartitions := make([]DiskPartitionInfo, 0)
	parts, _ := disk.Partitions(true)
	var diskUsage, diskTotal uint64
	var diskUsedPercent float64

	for _, part := range parts {
		if runtime.GOOS != "windows" {
			if strings.HasPrefix(part.Mountpoint, "/proc") ||
				strings.HasPrefix(part.Mountpoint, "/sys") ||
				strings.HasPrefix(part.Mountpoint, "/run") ||
				strings.HasPrefix(part.Mountpoint, "/dev") ||
				part.Fstype == "tmpfs" || part.Fstype == "devtmpfs" {
				continue
			}
		}
		stat, err := disk.Usage(part.Mountpoint)
		if err != nil {
			continue
		}
		skip := false
		for _, p := range diskPartitions {
			if p.MountPoint == stat.Path {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
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

	// 系统负载
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

	// 带宽计算
	now := time.Now()
	GlobalTrafficStats.mu.Lock()
	oldTotalIn := GlobalTrafficStats.InboundBytes
	oldTotalOut := GlobalTrafficStats.OutboundBytes
	timeDiff := now.Sub(GlobalTrafficStats.LastUpdate).Seconds()
	inboundBW, outboundBW := uint64(0), uint64(0)
	if timeDiff > 0 {
		inboundBW = uint64(float64(totalIn-oldTotalIn) / timeDiff)
		outboundBW = uint64(float64(totalOut-oldTotalOut) / timeDiff)
	}

	// 网络接口信息
	networkInterfaces := make([]NetworkInterfaceInfo, 0)
	for _, c := range counters {
		info := NetworkInterfaceInfo{
			Name:        c.Name,
			BytesRecv:   c.BytesRecv,
			BytesSent:   c.BytesSent,
			PacketsRecv: c.PacketsRecv,
			PacketsSent: c.PacketsSent,
		}
		if prev, ok := GlobalTrafficStats.PrevNetCounters[c.Name]; ok && timeDiff > 0 {
			info.RecvBandwidth = uint64(float64(c.BytesRecv-prev.BytesRecv) / timeDiff)
			info.SendBandwidth = uint64(float64(c.BytesSent-prev.BytesSent) / timeDiff)
		}
		networkInterfaces = append(networkInterfaces, info)
	}

	prevCounters := make(map[string]net.IOCountersStat)
	for _, c := range counters {
		prevCounters[c.Name] = c
	}
	GlobalTrafficStats.mu.Unlock()

	// 更新全局统计
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
	GlobalTrafficStats.InboundBandwidth = inboundBW
	GlobalTrafficStats.OutboundBandwidth = outboundBW
	GlobalTrafficStats.TotalBytes = totalIn + totalOut
	GlobalTrafficStats.LastUpdate = now

	// 更新应用自身统计
	updateAppStats(GlobalTrafficStats)
}

// -------------------- 应用自身统计 --------------------

func updateAppStats(ts *TrafficStats) {
	p, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		return
	}

	now := time.Now()

	// ---------------- CPU 使用率 ----------------
	cpuUsage := ts.App.CPUPercent
	cpuTimes, err := p.Times()
	if err == nil {
		if !ts.App.LastUpdate.IsZero() && ts.App.PrevCPUTime > 0 {
			duration := now.Sub(ts.App.LastUpdate).Seconds()
			if duration > 0 {
				usage := (cpuTimes.Total() - ts.App.PrevCPUTime) / duration * 100
				if usage < 0 {
					usage = 0
				}
				if usage > 100 {
					usage = 100
				}
				cpuUsage = usage
			}
		}
		ts.App.PrevCPUTime = cpuTimes.Total()
	}

	// ---------------- 内存 ----------------
	memInfo, _ := p.MemoryInfo()
	memUsage := uint64(0)
	if memInfo != nil {
		memUsage = memInfo.RSS
	}

	// ---------------- IO 流量 ----------------
	ioCounters, _ := p.IOCounters()
	inBytes, outBytes := uint64(0), uint64(0)
	if ioCounters != nil {
		inBytes = ioCounters.ReadBytes
		outBytes = ioCounters.WriteBytes
	}

	// ---------------- 实时带宽 & 累加 TotalBytes ----------------
	inBW, outBW := uint64(0), uint64(0)
	inDiff, outDiff := uint64(0), uint64(0)
	if !ts.App.LastUpdate.IsZero() {
		duration := now.Sub(ts.App.LastUpdate).Seconds()
		if duration > 0 && duration < 30 {
			if ts.App.PrevIOCounters != nil {
				inDiff = inBytes - ts.App.PrevIOCounters.ReadBytes
				outDiff = outBytes - ts.App.PrevIOCounters.WriteBytes
				inBW = uint64(float64(inDiff) / duration)
				outBW = uint64(float64(outDiff) / duration)
			}
		}
	}

	// ---------------- 更新 AppStats ----------------
	ts.App.CPUPercent = cpuUsage
	ts.App.MemoryUsage = memUsage
	ts.App.InboundBytes = inBytes
	ts.App.OutboundBytes = outBytes
	ts.App.InboundBandwidth = inBW
	ts.App.OutboundBandwidth = outBW
	ts.App.LastUpdate = now
	ts.App.PrevIOCounters = ioCounters

	// ---------------- 累加总流量 ----------------
	ts.App.TotalBytes += inDiff + outDiff
}
