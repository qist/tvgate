package upgrade

import (
	"fmt"
	"os"
	"net"
	"os/exec"
	"path/filepath"
	"time"
)

func NewExecPath() string {
	// 这里返回下载解压后的新程序路径，实际升级程序会放到 .tmp_upgrade 目录
	execPath, _ := os.Executable()
	tmpDir := filepath.Join(filepath.Dir(execPath), ".tmp_upgrade")
	return filepath.Join(tmpDir, filepath.Base(execPath))
}

var (
	socketPath = "/tmp/tvgate_upgrade.sock"
	listener   net.Listener
	onUpgrade  func()
)

// StartUpgradeListener 启动升级监听
// callback: 收到升级通知时调用
func StartUpgradeListener(callback func()) {
	onUpgrade = callback

	// 删除可能存在的旧 socket 文件
	if _, err := os.Stat(socketPath); err == nil {
		_ = os.Remove(socketPath)
	}

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		fmt.Printf("启动升级监听失败: %v\n", err)
		return
	}
	listener = ln
	fmt.Println("升级监听启动，socket:", socketPath)

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				fmt.Printf("升级监听 Accept 错误: %v\n", err)
				time.Sleep(1 * time.Second)
				continue
			}

			// 收到升级通知
			if onUpgrade != nil {
				onUpgrade()
			}
			_ = conn.Close()
		}
	}()
}

// 平滑升级主函数，供子进程调用
func RunUpgrader(oldPath, newPath, configPath, tmpDir string) {
	fmt.Printf("升级子进程启动: old=%s new=%s config=%s tmp=%s\n", oldPath, newPath, configPath, tmpDir)

	waitTimeout := 10 * time.Second
	start := time.Now()
	for {
		if _, err := os.Stat(oldPath); os.IsNotExist(err) {
			break
		}
		if time.Since(start) > waitTimeout {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	// 替换旧程序
	if err := os.Rename(newPath, oldPath); err != nil {
		fmt.Println("替换旧程序失败:", err)
		os.Exit(1)
	}
	fmt.Println("旧程序已替换为新程序")

	// 启动新程序
	cmd := exec.Command(oldPath, "-config="+configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Start(); err != nil {
		fmt.Println("启动新程序失败:", err)
		os.Exit(1)
	}
	fmt.Println("新程序已启动, PID:", cmd.Process.Pid)

	// 清理临时目录
	if tmpDir != "" {
		os.RemoveAll(tmpDir)
		fmt.Println("清理临时目录完成:", tmpDir)
	}

	os.Exit(0)
}

// NotifyUpgradeReady 通知旧程序准备升级
func NotifyUpgradeReady() error {
	if _, err := os.Stat(socketPath); err != nil {
		return fmt.Errorf("升级 socket 不存在: %v", err)
	}

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return fmt.Errorf("连接升级 socket 失败: %v", err)
	}
	defer conn.Close()
	return nil
}