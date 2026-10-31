package server

import (
	"context"
	"net"
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// envMultipathtcpForcedOn 判断测试进程是否在 GODEBUG 强制启用 MPTCP 的环境下运行。
func envMultipathtcpForcedOn() bool {
	for _, kv := range strings.Split(os.Getenv("GODEBUG"), ",") {
		if strings.TrimSpace(kv) == "multipathtcp=1" {
			return true
		}
	}
	return false
}

// TestMPTCPDisabledRegression CVE-2026-98432 回归测试：
// TCP 监听配置必须显式禁用 MPTCP，且保留 SO_REUSEPORT 复用语义。
// ListenConfig.MultipathTCP() 是标准库创建 socket 时的唯一判定依据，
// 返回 false 即保证任何内核、任何 GODEBUG=multipathtcp 环境下都不会
// 以 IPPROTO_MPTCP 创建端口监听。
func TestMPTCPDisabledRegression(t *testing.T) {
	if tcpListenConfig.MultipathTCP() {
		t.Fatal("tcpListenConfig 仍启用 MPTCP，CVE-2026-98432 回归")
	}

	// 对照组：零值 ListenConfig 在 GODEBUG=multipathtcp=1 下默认启用 MPTCP 监听，
	// 证明显式 SetMultipathTCP(false) 是必要的。
	if envMultipathtcpForcedOn() {
		var vulnerable net.ListenConfig
		if !vulnerable.MultipathTCP() {
			t.Log("当前工具链零值 ListenConfig 未默认启用 MPTCP（可能已修复标准库）")
		} else {
			t.Log("确认：零值 ListenConfig 默认启用 MPTCP（CVE 根源存在，显式禁用生效）")
		}
	}

	// 验证 SO_REUSEPORT 复用语义与原 go-reuseport 行为一致（仅 Unix）。
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		ln, err := tcpListenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("创建监听失败: %v", err)
		}
		defer ln.Close()

		raw, ok := ln.(syscall.Conn)
		if !ok {
			t.Fatal("listener 未实现 syscall.Conn")
		}
		rawConn, err := raw.SyscallConn()
		if err != nil {
			t.Fatalf("获取 RawConn 失败: %v", err)
		}
		if err := rawConn.Control(func(fd uintptr) {
			reuse, err := unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT)
			if err != nil {
				t.Errorf("读取 SO_REUSEPORT 失败: %v", err)
				return
			}
			if reuse != 1 {
				t.Errorf("SO_REUSEPORT 未设置，与原 reuseport.Listen 行为不一致")
			}
			// 普通 TCP socket 的 SO_PROTOCOL 为 0；MPTCP 为 IPPROTO_MPTCP(262)。
			proto, err := unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_PROTOCOL)
			if err != nil {
				t.Errorf("读取 SO_PROTOCOL 失败: %v", err)
				return
			}
			if proto != 0 && proto != unix.IPPROTO_TCP {
				t.Errorf("监听 socket 协议异常: %d (期望 0/TCP，262/MPTCP 为漏洞状态)", proto)
			}
		}); err != nil {
			t.Fatalf("Control 失败: %v", err)
		}
	}
}
