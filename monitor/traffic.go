package monitor

import (
	"math"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	// "fmt"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
)

// 全局进程对象，避免每次新建
var appProcess *process.Process

func getAppProcess() *process.Process {
	appProcessOnce.Do(func() {
		appProcess, _ = process.NewProcess(int32(os.Getpid()))
	})
	return appProcess
}

var appProcessOnce sync.Once

// -------------------- 数据结构 --------------------
// 将appProcess变量定义移到了文件开头的导入部分之后

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
	PrevCPUTime       float64 // ← 新增，用于计算 CPU 百分比
	CPUTemperature    float64 // 添加CPU温度字段
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
	SwapUsage       uint64 // SWAP使用量
	SwapTotal       uint64 // SWAP总量
	DiskUsage       uint64
	DiskTotal       uint64
	DiskUsedPercent float64
	DiskPartitions  []DiskPartitionInfo
	CPUTemperature  float64 // 添加CPU温度字段

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

// GetTrafficStats 获取流量统计信息的深拷贝
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
	appCopy := ts.App

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
		CPUTemperature:    ts.CPUTemperature,
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

func StartSystemStatsUpdater(interval time.Duration, stopChan chan struct{}) {
	ticker := time.NewTicker(interval)
	go func() {
		for {
			select {
			case <-ticker.C:
				updateSystemStats()
			case <-stopChan:
				ticker.Stop()
				return
			}
		}
	}()
}

var (
	lastCPUSample      time.Time
	cpuUsageCache      float64
	cpuCountCache      int
	lastCPUCountUpdate time.Time

	lastDiskScan    time.Time
	diskPartitions  []DiskPartitionInfo
	diskUsage       uint64
	diskTotal       uint64
	diskUsedPercent float64

	lastNetSample     time.Time
	networkInterfaces []NetworkInterfaceInfo
	totalIn           uint64
	totalOut          uint64
)

// 获取CPU温度（如果支持）
func getTemperature() float64 {
	temps, err := host.SensorsTemperatures()
	if err != nil || len(temps) == 0 {
		// fmt.Printf("DEBUG: Failed to get sensor temperatures: %v\n", err)
		return -1
	}

	// fmt.Printf("DEBUG: Got %d temperature sensors\n", len(temps))

	var cpuTemps []host.TemperatureStat
	for _, t := range temps {
		// fmt.Printf("DEBUG: Sensor %d: Key=%s, Temperature=%.2f°C\n", i, t.SensorKey, t.Temperature)
		key := strings.ToLower(t.SensorKey)
		if t.Temperature < 20 {
			continue
		}
		if strings.Contains(key, "cpu") ||
			strings.Contains(key, "core") ||
			strings.Contains(key, "package") ||
			strings.Contains(key, "tctl") ||
			strings.Contains(key, "tdie") {
			cpuTemps = append(cpuTemps, t)
		}
	}

	if len(cpuTemps) > 0 {
		maxTemp := cpuTemps[0].Temperature
		// bestSensor := cpuTemps[0].SensorKey
		for _, t := range cpuTemps {
			if t.Temperature > maxTemp {
				maxTemp = t.Temperature
				// bestSensor = t.SensorKey
			}
		}
		// fmt.Printf("DEBUG: Found CPU temperature sensor: %s = %.2f°C\n", bestSensor, maxTemp)
		return maxTemp
	}

	// 如果没有明确 CPU 传感器，取最高温度
	maxTemp := temps[0].Temperature
	// bestSensor := temps[0].SensorKey
	for _, t := range temps {
		if t.Temperature > maxTemp {
			maxTemp = t.Temperature
			// bestSensor = t.SensorKey
		}
	}
	// fmt.Printf("DEBUG: Using highest temperature sensor: %s = %.2f°C\n", bestSensor, maxTemp)
	return maxTemp
}

func updateSystemStats() {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	// CPU - 使用缓存减少频繁采样
	now := time.Now()

	// 获取CPU温度
	cpuTemperature := getTemperature()
	// fmt.Printf("DEBUG: CPU Temperature = %.2f°C\n", cpuTemperature)

	// 更新CPU核心数缓存(每小时更新一次)
	if now.Sub(lastCPUCountUpdate) > time.Hour {
		if count, err := cpu.Counts(true); err == nil {
			cpuCountCache = count
		}
		lastCPUCountUpdate = now
	}

	// CPU使用率采样(5秒间隔)
	var cpuUsage float64
	if now.Sub(lastCPUSample) > 5*time.Second {
		// 使用更短的采样时间(300ms)降低开销
		cpuPercent, _ := cpu.Percent(500*time.Millisecond, false)
		if len(cpuPercent) > 0 {
			rawUsage := cpuPercent[0]
			// 简化计算逻辑
			if cpuCountCache > 0 {
				rawUsage = rawUsage / float64(cpuCountCache)
			}
			// 限制范围并平滑变化
			if rawUsage > 0 {
				cpuUsageCache = math.Min(rawUsage, 100)
				// 如果变化小于1%，保持原值减少抖动
				if math.Abs(cpuUsageCache-rawUsage) < 1.0 {
					cpuUsageCache = rawUsage
				}
			} else {
				cpuUsageCache = 0
			}
			lastCPUSample = now
		}
	}
	cpuUsage = cpuUsageCache
	GlobalTrafficStats.CPUCount = cpuCountCache // 更新全局CPU核心数

	// 内存
	vmem, _ := mem.VirtualMemory()
	memUsage, memTotal := uint64(0), uint64(0)
	if vmem != nil {
		memUsage = vmem.Used
		memTotal = vmem.Total
	}

	// 磁盘 - 使用缓存减少频繁扫描
	if lastDiskScan.IsZero() {
		lastDiskScan = now.Add(-time.Hour)
	}
	if now.Sub(lastDiskScan) > 30*time.Second {
		parts, _ := disk.Partitions(true)
		tempPartitions := make([]DiskPartitionInfo, 0)
		var tempUsage, tempTotal uint64
		var tempUsedPercent float64
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
			for _, p := range tempPartitions {
				if p.MountPoint == stat.Path {
					skip = true
					break
				}
			}
			if skip {
				continue
			}
			if tempTotal == 0 {
				tempUsage = stat.Used
				tempTotal = stat.Total
				tempUsedPercent = stat.UsedPercent
			}
			tempPartitions = append(tempPartitions, DiskPartitionInfo{
				Path:        part.Device,
				Total:       stat.Total,
				Used:        stat.Used,
				Free:        stat.Free,
				UsedPercent: stat.UsedPercent,
				FsType:      part.Fstype,
				MountPoint:  stat.Path,
			})
		}
		diskPartitions = tempPartitions
		diskUsage = tempUsage
		diskTotal = tempTotal
		diskUsedPercent = tempUsedPercent
		lastDiskScan = now
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

	// 网络流量 - 使用缓存减少频繁采样
	if lastNetSample.IsZero() {
		lastNetSample = now.Add(-time.Hour)
	}
	if now.Sub(lastNetSample) > 1*time.Second {
		counters, _ := net.IOCounters(true)
		tempInterfaces := make([]NetworkInterfaceInfo, 0, len(counters))
		var tempIn, tempOut uint64
		for _, c := range counters {
			tempIn += c.BytesRecv
			tempOut += c.BytesSent
			info := NetworkInterfaceInfo{
				Name:        c.Name,
				BytesRecv:   c.BytesRecv,
				BytesSent:   c.BytesSent,
				PacketsRecv: c.PacketsRecv,
				PacketsSent: c.PacketsSent,
			}
			if prev, ok := GlobalTrafficStats.PrevNetCounters[c.Name]; ok {
				timeDiff := now.Sub(GlobalTrafficStats.LastUpdate).Seconds()
				if timeDiff > 0 {
					info.RecvBandwidth = uint64(float64(c.BytesRecv-prev.BytesRecv) / timeDiff)
					info.SendBandwidth = uint64(float64(c.BytesSent-prev.BytesSent) / timeDiff)
				}
			}
			tempInterfaces = append(tempInterfaces, info)
		}
		networkInterfaces = tempInterfaces
		totalIn = tempIn
		totalOut = tempOut
		lastNetSample = now
		// 带宽计算
		GlobalTrafficStats.mu.Lock()
		oldTotalIn := GlobalTrafficStats.InboundBytes
		oldTotalOut := GlobalTrafficStats.OutboundBytes
		timeDiff := now.Sub(GlobalTrafficStats.LastUpdate).Seconds()
		if timeDiff > 0 {
			GlobalTrafficStats.InboundBandwidth = uint64(float64(totalIn-oldTotalIn) / timeDiff)
			GlobalTrafficStats.OutboundBandwidth = uint64(float64(totalOut-oldTotalOut) / timeDiff)
		}
		prevCounters := make(map[string]net.IOCountersStat)
		for _, c := range counters {
			prevCounters[c.Name] = c
		}
		GlobalTrafficStats.PrevNetCounters = prevCounters
		GlobalTrafficStats.mu.Unlock()
	}

	// 更新全局统计
	GlobalTrafficStats.mu.Lock()
	defer GlobalTrafficStats.mu.Unlock()
	GlobalTrafficStats.CPUUsage = cpuUsage
	GlobalTrafficStats.CPUTemperature = cpuTemperature
	GlobalTrafficStats.MemoryUsage = memUsage
	GlobalTrafficStats.MemoryTotal = memTotal
	GlobalTrafficStats.DiskUsage = diskUsage
	GlobalTrafficStats.DiskTotal = diskTotal
	GlobalTrafficStats.DiskUsedPercent = diskUsedPercent
	GlobalTrafficStats.DiskPartitions = diskPartitions
	GlobalTrafficStats.LoadAverage = loadAverage
	GlobalTrafficStats.HostInfo = hostDetails
	GlobalTrafficStats.NetworkInterfaces = networkInterfaces
	GlobalTrafficStats.InboundBytes = totalIn
	GlobalTrafficStats.OutboundBytes = totalOut
	GlobalTrafficStats.TotalBytes = totalIn + totalOut
	GlobalTrafficStats.LastUpdate = now

	// 更新应用自身统计
	updateAppStats(GlobalTrafficStats)
}

// -------------------- 应用自身统计 --------------------

func updateAppStats(ts *TrafficStats) {
	p := getAppProcess()
	if p == nil {
		return
	}

	now := time.Now()

	// ---------------- CPU 使用率 ----------------
	cpuUsage := ts.App.CPUPercent
	cpuTimes, err := p.Times()
	if err == nil {
		// 推荐用 User+System 字段代替 Total()
		currentCPUTime := cpuTimes.User + cpuTimes.System
		if !ts.App.LastUpdate.IsZero() && ts.App.PrevCPUTime > 0 {
			duration := now.Sub(ts.App.LastUpdate).Seconds()
			if duration > 0 {
				usage := (currentCPUTime - ts.App.PrevCPUTime) / duration * 100
				if usage < 0 {
					usage = 0
				}
				if usage > 100 {
					usage = 100
				}
				cpuUsage = usage
			}
		}
		ts.App.PrevCPUTime = currentCPUTime
	}

	// ---------------- 内存 ----------------
	memUsage := uint64(0)
	if memStats, err := p.MemoryInfo(); err == nil {
		memUsage = memStats.RSS
	}

	// ---------------- IO 流量 ----------------
	// inBytes, outBytes := uint64(0), uint64(0)
	// if ioCounters, err := p.IOCounters(); err == nil {
	// 	inBytes = ioCounters.ReadBytes
	// 	outBytes = ioCounters.WriteBytes
	// }

	// ---------------- 实时带宽 & 累加 TotalBytes ----------------
	// inBW, outBW := uint64(0), uint64(0)
	// inDiff, outDiff := uint64(0), uint64(0)
	// if !ts.App.LastUpdate.IsZero() {
	// 	duration := now.Sub(ts.App.LastUpdate).Seconds()
	// 	if duration > 0 && duration < 30 {
	// 		if ts.App.PrevIOCounters != nil {
	// 			inDiff = inBytes - ts.App.PrevIOCounters.ReadBytes
	// 			outDiff = outBytes - ts.App.PrevIOCounters.WriteBytes
	// 			inBW = uint64(float64(inDiff) / duration)
	// 			outBW = uint64(float64(outDiff) / duration)
	// 		}
	// 	}
	// }

	// ---------------- 更新 AppStats ----------------
	// fmt.Printf("DEBUG: App Stats - CPU: %.2f%%, Memory: %d bytes\n", cpuUsage, memUsage)
	ts.App.CPUPercent = cpuUsage
	ts.App.MemoryUsage = memUsage
	// ts.App.InboundBytes = inBytes
	// ts.App.OutboundBytes = outBytes
	// ts.App.InboundBandwidth = inBW
	// ts.App.OutboundBandwidth = outBW
	ts.App.LastUpdate = now
	// ts.App.PrevIOCounters = &process.IOCountersStat{
	// ReadBytes:  inBytes,
	// WriteBytes: outBytes,
	// }

	// ---------------- 累加总流量 ----------------
	// ts.App.TotalBytes += inDiff + outDiff
}
