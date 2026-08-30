//go:build unix

package phpgo

import "syscall"

// flockFd 对 fd 执行文件锁。
// PHP 常量：LOCK_SH=1 LOCK_EX=2 LOCK_UN=3 LOCK_NB=4（与 syscall 值不同，需映射）。
// 非法操作返回错误（PHP flock 返回 false）。
func flockFd(fd uintptr, op int) error {
	var how int
	switch op &^ 4 {
	case 1:
		how = syscall.LOCK_SH
	case 2:
		how = syscall.LOCK_EX
	case 3:
		how = syscall.LOCK_UN
	default:
		return errInvalidFlockOp
	}
	if op&4 != 0 {
		how |= syscall.LOCK_NB
	}
	return syscall.Flock(int(fd), how)
}

var errInvalidFlockOp = &invalidFlockOpError{}

type invalidFlockOpError struct{}

func (*invalidFlockOpError) Error() string { return "invalid flock operation" }
