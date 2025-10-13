//go:build windows
// +build windows

package publisher

import (
	"syscall"
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