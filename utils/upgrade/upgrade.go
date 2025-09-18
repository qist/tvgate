package upgrade

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

var (
	listener  net.Listener
	onUpgrade func()
	mu        sync.Mutex
)

// NewExecPath 返回下载解压后的新程序临时路径
func NewExecPath() (string, string) {
	tmpDir := filepath.Join(os.TempDir(), "tvgate_upgrade")
	_ = os.MkdirAll(tmpDir, 0755)

	execName := filepath.Base(os.Args[0])
	execPath := filepath.Join(tmpDir, execName)
	return execPath, tmpDir
}

// SocketPath 返回临时 socket 文件路径
func SocketPath() string {
	tmpDir := filepath.Join(os.TempDir(), "tvgate_upgrade")
	return filepath.Join(tmpDir, "tvgate_upgrade.sock")
}

// StartUpgradeListener 启动升级监听
func StartUpgradeListener(callback func()) {
	onUpgrade = callback

	socketPath := SocketPath()

	// 确保 socket 所在目录存在
	if err := os.MkdirAll(filepath.Dir(socketPath), 0755); err != nil {
		fmt.Printf("创建 socket 目录失败: %v\n", err)
		return
	}

	// 删除可能存在的旧 socket 文件
	if _, err := os.Stat(socketPath); err == nil {
		_ = os.Remove(socketPath)
	}

	var err error
	listener, err = net.Listen("unix", socketPath)
	if err != nil {
		fmt.Printf("启动升级监听失败: %v\n", err)
		return
	}

	fmt.Println("升级监听启动，socket:", socketPath)

	go func() {
		defer func() {
			listener.Close()
			fmt.Println("升级监听已关闭")
		}()
		for {
			conn, err := listener.Accept()
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Temporary() {
					time.Sleep(time.Second)
					continue
				}
				return
			}

			go func(c net.Conn) {
				defer c.Close()
				if onUpgrade != nil {
					onUpgrade()
				}
				c.Write([]byte("OK"))
			}(conn)
		}
	}()
}


// StopUpgradeListener 关闭升级监听
func StopUpgradeListener() {
	mu.Lock()
	defer mu.Unlock()
	if listener != nil {
		_ = listener.Close()
		listener = nil
	}
}

// RunUpgrader 平滑升级主函数
func RunUpgrader(oldPath, newPath, configPath, tmpDir string) {
	fmt.Printf("升级子进程启动: old=%s new=%s\n", oldPath, newPath)

	// 关闭升级监听，释放 socket
	StopUpgradeListener()

	// 删除旧程序
	_ = os.Remove(oldPath)

	// 复制新程序到旧程序位置
	if err := copyFile(newPath, oldPath); err != nil {
		fmt.Printf("复制新程序失败: %v\n", err)
		os.Exit(1)
	}

	// 临时文件复制完成后立即清理临时目录
	if tmpDir != "" {
		_ = os.RemoveAll(tmpDir)
		fmt.Printf("临时升级目录已清理: %s\n", tmpDir)
	}

	// 设置可执行权限
	_ = os.Chmod(oldPath, 0755)

	// 启动新程序
	cmd := exec.Command(oldPath, "-config="+configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Start(); err != nil {
		fmt.Printf("启动新程序失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("新程序已启动, PID: %d\n", cmd.Process.Pid)

	os.Exit(0)
}

// copyFile 复制文件
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

// NotifyUpgradeReady 通知旧程序准备升级
func NotifyUpgradeReady() error {
	socketPath := SocketPath()
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		return fmt.Errorf("连接升级 socket 失败: %v", err)
	}
	defer conn.Close()

	buf := make([]byte, 2)
	_, err = conn.Read(buf)
	if err != nil && err != io.EOF {
		return fmt.Errorf("读取确认信息失败: %v", err)
	}

	return nil
}
