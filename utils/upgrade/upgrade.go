package upgrade

import (
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
