//go:build !unix

package phpgo

// flockFd 在无 flock 的系统调用平台（Windows / plan9 / js / wasip1 等）上为 no-op，
// 返回 nil 表示"加锁成功"，保证依赖 flock 的 PHP 脚本在这些平台不因缺少锁而报错。
// 注意：此时跨进程文件锁互斥语义不生效（单进程内脚本仍按顺序执行）。
func flockFd(fd uintptr, op int) error {
	return nil
}
