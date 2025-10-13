//go:build !windows
// +build !windows

package publisher

import (
	"syscall"
)

func setSysProcAttr(cmd *syscall.SysProcAttr) {
	cmd.Setpgid = true
}

func killProcess(pid int) error {
    return syscall.Kill(-pid, syscall.SIGKILL)
}