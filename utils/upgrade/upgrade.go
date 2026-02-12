package upgrade

import (
	"io"
	"runtime"

	// "log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/cloudflare/tableflip"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/stream"
	tsync "github.com/qist/tvgate/utils/sync"
)

var upgradeWg tsync.WaitGroup

var (
	upgrader  *tableflip.Upgrader
	once      sync.Once
	isWindows = runtime.GOOS == "windows"
	sigMu     sync.Mutex
	sigChan   chan os.Signal
	closeMu   sync.Mutex
	closeFn   func()
)

// var (
// 	serviceMode     string
// 	serviceModeOnce sync.Once
// )

// Get 全局唯一升级器
func Get() *tableflip.Upgrader {
	Init()
	if isWindows {
		// logger.LogPrintf("当前平台不支持 tableflip 热升级，采用普通重启")
		return nil
	}
	return upgrader
}

// Init 初始化升级器，只会执行一次
func Init() {
	once.Do(func() {
		if isWindows {
			// logger.LogPrintf("Windows 平台不初始化 tableflip，采用普通重启")
			return
		}
		var err error
		upgrader, err = tableflip.New(tableflip.Options{})
		if err != nil {
			logger.LogPrintf("创建升级器失败: %v", err)
		}
	})
}

// StartListener 监听 SIGHUP 信号执行热更新
func StartListener(onUpgrade func()) {
	Init()
	if isWindows {
		// logger.LogPrintf("Windows 平台不支持 SIGHUP 热升级监听，跳过 tableflip")
		return
	}
	sigMu.Lock()
	if sigChan != nil {
		sigMu.Unlock()
		return
	}
	sigChan = make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGHUP)
	sigMu.Unlock()

	upgradeWg.Go(func() {
		for range sigChan {
			logger.LogPrintf("收到升级信号，开始优雅退出...")

			// 关闭 StreamHub
			stream.GlobalMultiChannelHub.Mu.Lock()
			for key, hub := range stream.GlobalMultiChannelHub.Hubs {
				hub.Close()
				delete(stream.GlobalMultiChannelHub.Hubs, key)
			}
			stream.GlobalMultiChannelHub.Mu.Unlock()

			if onUpgrade != nil {
				onUpgrade()
			}

			if err := upgrader.Upgrade(); err != nil {
				logger.LogPrintf("升级失败: %v", err)
			}
		}
	})
}

// StopUpgradeListener 停止升级监听
func StopUpgradeListener() {
	sigMu.Lock()
	ch := sigChan
	sigChan = nil
	sigMu.Unlock()
	if ch == nil {
		return
	}
	signal.Stop(ch)
	close(ch)
}

func SetCloseServers(fn func()) {
	closeMu.Lock()
	closeFn = fn
	closeMu.Unlock()
}

// Ready 标记子进程已准备好接管
func Ready() {
	Init()
	if isWindows {
		// logger.LogPrintf("Windows 平台无需 Ready() 热升级标记")
		return
	}
	if err := upgrader.Ready(); err != nil {
		logger.LogPrintf("升级器准备失败: %v", err)
	}
}

// Exit 清理旧进程
func Exit() {
	if isWindows {
		// logger.LogPrintf("Windows 平台无需升级器 Exit()，直接退出")
		os.Exit(0)
		return
	}
	if err := upgrader.Exit(); err != nil {
		logger.LogPrintf("旧进程退出失败: %v", err)
	}
}

// UpgradeProcess 复制新文件、清理临时目录并启动新进程
func UpgradeProcess(newExecPath, configPath, tmpDir string) {
	execPath := os.Args[0]

	// 关闭升级监听，释放 socket
	StopUpgradeListener()

	closeMu.Lock()
	fn := closeFn
	closeMu.Unlock()
	if fn != nil {
		fn()
	}

	stream.GlobalMultiChannelHub.Mu.Lock()
	for key, hub := range stream.GlobalMultiChannelHub.Hubs {
		hub.Close()
		delete(stream.GlobalMultiChannelHub.Hubs, key)
	}
	stream.GlobalMultiChannelHub.Mu.Unlock()

	// 删除旧程序
	_ = os.Remove(execPath)

	// 复制新程序到旧程序位置
	if err := copyFile(newExecPath, execPath); err != nil {
		logger.LogPrintf("复制新程序失败: %v", err)
	}

	// 临时文件复制完成后立即清理临时目录
	if tmpDir != "" {
		_ = os.RemoveAll(tmpDir)
		logger.LogPrintf("临时升级目录已清理: %s", tmpDir)
	}

	// 设置可执行权限
	_ = os.Chmod(execPath, 0755)

	// 检查是否 systemd/procd 管理（可选，仅用于日志）
	isManaged := false
	if pName, err := getProcessName(os.Getppid()); err == nil {
		if strings.Contains(pName, "systemd") || strings.Contains(pName, "procd") {
			isManaged = true
		}
	}

	if isManaged {
		logger.LogPrintf("检测到 systemd/procd 管理，旧进程退出，由管理器拉起新进程")
		// 直接退出旧进程即可
		os.Exit(0)
	} else {
		// 单进程模式 → 启动新程序
		cmd := exec.Command(execPath, "-config="+configPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		if err := cmd.Start(); err != nil {
			logger.LogPrintf("启动新程序失败: %v\n", err)
		}
		os.Exit(0)
	}
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

// getProcessName 只在检测父进程时调用，Linux下从 /proc 读取进程名
func getProcessName(pid int) (string, error) {
	data, err := os.ReadFile("/proc/" + itoa(pid) + "/comm")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// 简单整型转字符串，避免 fmt.Sprintf 调用开销
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
