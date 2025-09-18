package upgrade

import (
	"fmt"
	"net"
	"os"
	"syscall"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

var (
	socketPath string
	stopFunc   func()
)

// StartUpgradeListener 旧程序启动时调用，监听本地 Socket 通知
func StartUpgradeListener(onUpgrade func()) {
	stopFunc = onUpgrade

	if runtime.GOOS == "windows" {
		socketPath = "127.0.0.1:53013" // Windows 用本地 TCP
	} else {
		socketPath = "/tmp/tvgate_upgrade.sock"
		os.Remove(socketPath)
	}

	var l net.Listener
	var err error
	if runtime.GOOS == "windows" {
		l, err = net.Listen("tcp", socketPath)
	} else {
		l, err = net.Listen("unix", socketPath)
	}
	if err != nil {
		panic(err)
	}

	go func() {
		defer l.Close()
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				// 发送确认信息
				c.Write([]byte("OK"))
				c.Close()
				time.Sleep(500 * time.Millisecond) // 延迟退出，保证新程序绑定端口
				if stopFunc != nil {
					stopFunc()
				}
			}(conn)
		}
	}()
}

// NotifyUpgradeReady 新程序下载完成后调用，通知旧程序退出
func NotifyUpgradeReady() error {
	var conn net.Conn
	var err error
	
	// 设置连接超时
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
	}
	
	if runtime.GOOS == "windows" {
		conn, err = dialer.Dial("tcp", socketPath)
	} else {
		conn, err = dialer.Dial("unix", socketPath)
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	
	// 设置读取超时
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	
	// 读取确认信息
	buf := make([]byte, 2)
	_, err = conn.Read(buf)
	if err != nil {
		return err
	}
	
	// 检查确认信息
	if string(buf) != "OK" {
		return fmt.Errorf("收到无效确认信息: %s", string(buf))
	}
	
	return nil
}

// RestartProcess 下载解压完成后调用
func RestartProcess(execPath string) error {
	args := append([]string{execPath}, os.Args[1:]...)
	env := os.Environ()

	if runtime.GOOS != "windows" {
		// Linux/macOS 直接替换进程
		return syscallExec(execPath, args, env)
	}

	// Windows 非阻塞启动新进程
	cmd := exec.Command(execPath, os.Args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Dir = filepath.Dir(execPath)
	cmd.Env = env

	if err := cmd.Start(); err != nil {
		return err
	}

	// 退出旧程序
	os.Exit(0)
	return nil
}

func syscallExec(execPath string, args, env []string) error {
	return syscall.Exec(execPath, args, env)
}
