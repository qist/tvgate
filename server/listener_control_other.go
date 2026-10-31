//go:build !unix

package server

import "syscall"

// reusePortControl 非 Unix 平台（Windows 等）不支持 SO_REUSEPORT，
// 与原 github.com/libp2p/go-reuseport 的 Control 行为保持一致（空操作）。
func reusePortControl(string, string, syscall.RawConn) error {
	return nil
}
