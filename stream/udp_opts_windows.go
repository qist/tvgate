//go:build windows

package stream

import "syscall"

func setReuse(fd uintptr) {
	handle := syscall.Handle(fd)
	_ = syscall.SetsockoptInt(handle, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
	// Windows 没有 SO_REUSEPORT
}
