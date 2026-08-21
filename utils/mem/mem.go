package mem

import (
	"runtime"

	"github.com/qist/tvgate/logger"
)

// FreeMemory 主动触发一次常规 GC。
//
// 注意：旧实现调用了 debug.FreeOSMemory()，它会将空闲内存归还给操作系统并
// 清空 runtime 的堆内存缓存（含 sync.Pool 的 victim 缓存）。在高频的 Hub
// 关闭路径上反复调用会导致：后续内存分配变慢、GC 频率升高、RSS 持续抖动，
// 反而同时抬高 CPU 与内存占用。因此这里只做普通 GC，不做 FreeOSMemory。
//
// 仅在确实需要压低 RSS（如长时间空闲）的少数场景使用，常规关闭路径不应调用。
func FreeMemory() {
	runtime.GC()
}

// FreeMemoryIfHigh 当 Go 堆使用超过 thresholdMB 时才触发一次常规 GC，
// 避免频繁 GC 影响性能。thresholdMB <= 0 时总是触发。
func FreeMemoryIfHigh(thresholdMB int) {
	if thresholdMB <= 0 {
		FreeMemory()
		return
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapMB := int(m.HeapAlloc / (1024 * 1024))
	if heapMB >= thresholdMB {
		logger.LogPrintf("内存回收: heap=%dMB, 触发GC", heapMB)
		FreeMemory()
	}
}
