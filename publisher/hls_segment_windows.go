//go:build windows
// +build windows

package publisher

import (
	"syscall"
	"time"
    // "path/filepath"
	"unsafe"
    "os"
)

func setSysProcAttr(cmd *syscall.SysProcAttr) {
	// Windows不支持Setpgid
}

func killProcess(pid int) error {
	// Windows 实现 - 使用 taskkill 命令或者 Windows API
	// 这里使用较为简单的 Windows API 方式
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	openProcess := kernel32.NewProc("OpenProcess")
	terminateProcess := kernel32.NewProc("TerminateProcess")
	closeHandle := kernel32.NewProc("CloseHandle")

	// 打开进程
	process, _, _ := openProcess.Call(0x0001, 0, uintptr(pid))
	if process == 0 {
		return nil // 进程可能已经不存在
	}
	defer closeHandle.Call(process)

	// 终止进程
	terminateProcess.Call(process, 0)
	return nil
}

func GetFileCreateTime(info os.FileInfo, path string) time.Time {
    p, err := syscall.UTF16PtrFromString(path)
    if err != nil {
        return info.ModTime()
    }
    
    var data syscall.Win32FileAttributeData
    err = syscall.GetFileAttributesEx(p, syscall.GetFileExInfoStandard, (*byte)(unsafe.Pointer(&data)))
    if err != nil {
        return info.ModTime()
    }
    
    ft := data.CreationTime
    // 直接使用 Nanoseconds() 方法，然后转换为 time.Time
    return time.Unix(0, ft.Nanoseconds())
}