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
	gopsutilmem "github.com/shirou/gopsutil/v3/mem"
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
	SwapUsage       uint64  // SWAP使用量
	SwapTotal       uint64  // SWAP总量
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
	defer ticker.Stop()
	// 内存回收检查间隔：每 60 秒检查一次
	memCheckTicker := time.NewTicker(60 * time.Second)
	defer memCheckTicker.Stop()
	for {
		select {
		case <-ticker.C:
			updateSystemStats()
		case <-memCheckTicker.C:
			// 仅在堆内存显著偏高时做一次常规 GC。
			// 注意：不要调用 debug.FreeOSMemory()，它会把空闲内存归还 OS
			// 并清空 sync.Pool victim 缓存，反而导致后续分配变慢、GC 更频繁。
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			if m.HeapAlloc > 256*1024*1024 {
				runtime.GC()
			}
		case <-stopChan:
			return
		}
	}
}

var (
	lastCPUSample      time.Time
	cpuUsageCache      float64
	cpuCountCache      int
	lastCPUCountUpdate time.Time
	prevCPUTimes       cpu.TimesStat // 非阻塞 CPU 使用率差分计算的上一拍快照
	havePrevCPUTimes   bool

	// 内存/负载缓存，避免每次 tick 都做 syscalls
	lastMemSample time.Time
	memUsageCache uint64
	memTotalCache uint64

	lastLoadSample time.Time
	loadCache      LoadAverageInfo

	lastHostSample time.Time // 已用 hostInfoCached 控制，保留兼容

	// 磁盘缓存
	lastDiskScan    time.Time
	diskPartitions  []DiskPartitionInfo
	diskUsage       uint64
	diskTotal       uint64
	diskUsedPercent float64

	// 网络流量缓存
	lastNetSample     time.Time
	networkInterfaces []NetworkInterfaceInfo
	totalIn           uint64
	totalOut          uint64

	// 主机信息缓存（静态信息，只需获取一次）
	cachedHostOS           string
	cachedHostPlatform     string
	cachedHostKernelArch   string
	cachedHostKernelVersion string
	hostInfoCached         bool

	// 温度缓存
	lastTemperatureSample time.Time
	cachedTemperature     float64
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
	// CPU - 使用缓存减少频繁采样
	now := time.Now()

	// 获取CPU温度（每30秒更新一次，避免频繁读取传感器）
	if now.Sub(lastTemperatureSample) > 30*time.Second {
		cachedTemperature = getTemperature()
		lastTemperatureSample = now
	}
	cpuTemperature := cachedTemperature
	// fmt.Printf("DEBUG: CPU Temperature = %.2f°C\n", cpuTemperature)

	// 更新CPU核心数缓存(每小时更新一次)
	if now.Sub(lastCPUCountUpdate) > time.Hour {
		if count, err := cpu.Counts(true); err == nil {
			cpuCountCache = count
		}
		lastCPUCountUpdate = now
	}

	// CPU使用率采样：使用非阻塞的 cpu.Times() 差分计算，
	// 避免 cpu.Percent() 内部阻塞 300ms 拖慢整个监控 goroutine。
	var cpuUsage float64
	if now.Sub(lastCPUSample) > 5*time.Second {
		times, err := cpu.Times(false) // 整体，不阻塞
		if err == nil && len(times) > 0 {
			t := times[0]
			if havePrevCPUTimes {
				prevTotal := prevCPUTimes.User + prevCPUTimes.System + prevCPUTimes.Idle +
					prevCPUTimes.Nice + prevCPUTimes.Iowait + prevCPUTimes.Irq +
					prevCPUTimes.Softirq + prevCPUTimes.Steal
				curTotal := t.User + t.System + t.Idle + t.Nice + t.Iowait +
					t.Irq + t.Softirq + t.Steal
				dt := curTotal - prevTotal
				if dt > 0 {
					dBusy := (curTotal - prevTotal) - (t.Idle - prevCPUTimes.Idle)
					rawUsage := dBusy / dt * 100
					if rawUsage > 0 {
						cpuUsageCache = math.Min(rawUsage, 100)
					} else {
						cpuUsageCache = 0
					}
				}
			}
			prevCPUTimes = t
			havePrevCPUTimes = true
			lastCPUSample = now
		}
	}
	cpuUsage = cpuUsageCache
	GlobalTrafficStats.CPUCount = cpuCountCache // 更新全局CPU核心数

	// 内存：30秒采样一次，避免每次 tick 都做 syscalls
	var memUsage, memTotal uint64
	if now.Sub(lastMemSample) > 30*time.Second {
		if vmem, err := gopsutilmem.VirtualMemory(); err == nil && vmem != nil {
			memUsageCache = vmem.Used
			memTotalCache = vmem.Total
		}
		lastMemSample = now
	}
	memUsage = memUsageCache
	memTotal = memTotalCache

	// 磁盘 - 使用缓存减少频繁扫描

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

	// 系统负载：30秒采样一次
	var loadAverage LoadAverageInfo
	if now.Sub(lastLoadSample) > 30*time.Second {
		if loadAvg, err := load.Avg(); err == nil && loadAvg != nil {
			loadCache.Load1 = loadAvg.Load1
			loadCache.Load5 = loadAvg.Load5
			loadCache.Load15 = loadAvg.Load15
		}
		lastLoadSample = now
	}
	loadAverage = loadCache

	// 主机信息（静态信息，只需获取一次）
	if !hostInfoCached {
		if hostInfo, err := host.Info(); err == nil && hostInfo != nil {
			cachedHostOS = hostInfo.OS
			cachedHostPlatform = hostInfo.Platform
			cachedHostKernelArch = hostInfo.KernelArch
			cachedHostKernelVersion = hostInfo.KernelVersion
		}
		// Android 等平台 gopsutil 可能获取不到系统信息，用 runtime 兜底
		if cachedHostOS == "" {
			cachedHostOS = runtime.GOOS
		}
		if cachedHostPlatform == "" {
			if runtime.GOOS == "android" {
				cachedHostPlatform = "Android"
			} else {
				cachedHostPlatform = runtime.GOOS
			}
		}
		if cachedHostKernelArch == "" {
			cachedHostKernelArch = runtime.GOARCH
		}
		if cachedHostKernelVersion == "" {
			// 尝试从 /proc/version 读取内核版本（Linux/Android）
			if data, err := os.ReadFile("/proc/version"); err == nil {
				// 格式: Linux version 5.10.101 (gcc...) #1 SMP ...
				fields := strings.Fields(string(data))
				if len(fields) >= 3 {
					cachedHostKernelVersion = fields[2]
				}
			}
		}
		hostInfoCached = true
	}
	hostDetails := HostInfo{
		OS:           cachedHostOS,
		Platform:     cachedHostPlatform,
		KernelArch:   cachedHostKernelArch,
		KernelVersion: cachedHostKernelVersion,
	}

	// 网络流量 - 使用缓存减少频繁采样

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
