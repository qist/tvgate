package mem

import (
	"runtime"
	"runtime/debug"

	"github.com/qist/tvgate/logger"
)

// FreeMemory 主动触发 GC 并将空闲内存归还给操作系统。
// 在流关闭、Hub 销毁等关键清理路径调用，避免 RSS 持续偏高。
func FreeMemory() {
	runtime.GC()
	debug.FreeOSMemory()
}

// FreeMemoryIfHigh 当 Go 堆使用超过 thresholdMB 时才触发 GC + FreeOSMemory，
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
		logger.LogPrintf("内存回收: heap=%dMB, 触发GC+FreeOSMemory", heapMB)
		FreeMemory()
	}
}
