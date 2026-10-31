//go:build unix

package server

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// reusePortControl 为监听 socket 设置 SO_REUSEADDR/SO_REUSEPORT，
// 与原 github.com/libp2p/go-reuseport 的 Control 行为保持一致。
func reusePortControl(network, address string, c syscall.RawConn) (err error) {
	controlErr := c.Control(func(fd uintptr) {
		err = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
		if err != nil {
			return
		}
		err = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
	})
	if controlErr != nil {
		err = controlErr
	}
	return
}
