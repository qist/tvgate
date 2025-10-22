//go:build !windows
// +build !windows

package publisher

import (
	"syscall"
	"os"
	"time"
)

func setSysProcAttr(cmd *syscall.SysProcAttr) {
	cmd.Setpgid = true
}

func killProcess(pid int) error {
    return syscall.Kill(-pid, syscall.SIGKILL)
}
func GetFileCreateTime(info os.FileInfo, path string) time.Time {
    return info.ModTime()
}