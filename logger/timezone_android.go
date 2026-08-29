//go:build android

// logger/timezone_android.go
// Android 专用：Go 在 Android 上读不到 /etc/localtime 与 TZ 环境变量，
// time.Local 默认 UTC，导致日志/时间输出为 UTC。
// 通过 cgo 读取系统属性 persist.sys.timezone（如 Asia/Shanghai），
// 再用 time.LoadLocation 加载并设为 time.Local，使 time.Now() 输出设备本地时间。
// 注：IANA 时区库由 phpgo 的 _ "time/tzdata" 内嵌，此处无需重复导入。
package logger

import (
	"time"
	"unsafe"
)

/*
#include <sys/system_properties.h>
#include <stdlib.h>

static int android_get_prop(const char *name, char *buf, int len) {
	return __system_property_get(name, buf);
}
*/
import "C"

func init() {
	tz := androidSystemTimezone()
	if tz == "" {
		return
	}
	if loc, err := time.LoadLocation(tz); err == nil {
		time.Local = loc
	}
}

func androidSystemTimezone() string {
	name := C.CString("persist.sys.timezone")
	defer C.free(unsafe.Pointer(name))

	buf := make([]byte, 64)
	n := C.android_get_prop(name, (*C.char)(unsafe.Pointer(&buf[0])), C.int(len(buf)))
	if n <= 0 {
		return ""
	}
	return string(buf[:n])
}
