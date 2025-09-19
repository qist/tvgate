package upgrade

import (
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"

	"github.com/cloudflare/tableflip"
	"github.com/qist/tvgate/stream"
)

var (
	upgrader *tableflip.Upgrader
	once     sync.Once
)

// Get 全局唯一升级器
func Get() *tableflip.Upgrader {
	Init()
	return upgrader
}

// Init 初始化升级器，只会执行一次
func Init() {
	once.Do(func() {
		var err error
		upgrader, err = tableflip.New(tableflip.Options{})
		if err != nil {
			log.Fatalf("创建升级器失败: %v", err)
		}
	})
}

// StartListener 监听 SIGHUP 信号执行热更新
func StartListener(onUpgrade func()) {
	Init()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGHUP)

	go func() {
		for range sigChan {
			log.Println("收到升级信号，开始优雅退出...")

			// 关闭 StreamHub
			stream.HubsMu.Lock()
			for key, hub := range stream.Hubs {
				hub.Close()
				delete(stream.Hubs, key)
			}
			stream.HubsMu.Unlock()

			if onUpgrade != nil {
				onUpgrade()
			}

			if err := upgrader.Upgrade(); err != nil {
				log.Printf("升级失败: %v", err)
			}
		}
	}()
}

// StopUpgradeListener 停止升级监听
func StopUpgradeListener() {
	signal.Stop(make(chan os.Signal, 1))
}

// Ready 标记子进程已准备好接管
func Ready() {
	Init()
	if err := upgrader.Ready(); err != nil {
		log.Fatalf("升级器准备失败: %v", err)
	}
}

// Exit 清理旧进程
func Exit() {
	if err := upgrader.Exit(); err != nil {
		log.Printf("旧进程退出失败: %v", err)
	}
}

// UpgradeProcess 复制新文件、清理临时目录并启动新进程
func UpgradeProcess(newExecPath, configPath, tmpDir string) {
	execPath := os.Args[0]
	
	// 关闭升级监听，释放 socket
	StopUpgradeListener()
	
	// 删除旧程序
	_ = os.Remove(execPath)
	
	// 复制新程序到旧程序位置
	if err := copyFile(newExecPath, execPath); err != nil {
		log.Fatalf("复制新程序失败: %v", err)
	}
	
	// 临时文件复制完成后立即清理临时目录
	if tmpDir != "" {
		_ = os.RemoveAll(tmpDir)
		log.Printf("临时升级目录已清理: %s", tmpDir)
	}
	
	// 设置可执行权限
	_ = os.Chmod(execPath, 0755)
	
	// 启动新程序
	cmd := exec.Command(execPath, "-config="+configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Start(); err != nil {
		log.Fatalf("启动新程序失败: %v", err)
	}
	log.Printf("新程序已启动, PID: %d", cmd.Process.Pid)
	
	os.Exit(0)
}

// RunNewProcess 启动新进程
func RunNewProcess(configPath string) {
	execPath := os.Args[0]
	cmd := exec.Command(execPath, "-config="+configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Start(); err != nil {
		log.Fatalf("启动新程序失败: %v", err)
	}

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
