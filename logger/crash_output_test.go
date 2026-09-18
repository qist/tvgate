package logger

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const crashMarker = "崩溃输出落盘测试-CRASH-MARKER"

// 崩溃输出必须落到日志文件：panic 堆栈走的是 fd 2（stderr），只靠启动脚本的 2>&1 才看得到，
// systemd / 后台面板 / 容器启动就丢，而"进程莫名消失"时这条堆栈恰恰是唯一线索。
// 这里起子进程让它 panic，断言堆栈进了配置的日志文件。
func TestCrashOutputGoesToLogFile(t *testing.T) {
	if os.Getenv("TVGATE_CRASH_LOG_CHILD") != "" {
		// 子进程分支：配置日志文件后崩溃（父进程断言其内容）
		SetupLogger(LogConfig{Enabled: true, File: os.Getenv("TVGATE_CRASH_LOG_FILE")})
		panic(crashMarker)
	}

	logPath := filepath.Join(t.TempDir(), "crash.log")
	cmd := exec.Command(os.Args[0], "-test.run=TestCrashOutputGoesToLogFile")
	cmd.Env = append(os.Environ(),
		"TVGATE_CRASH_LOG_CHILD=1",
		"TVGATE_CRASH_LOG_FILE="+logPath,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("子进程应因 panic 非零退出:\n%s", out)
	}

	data, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatalf("读日志文件失败: %v", readErr)
	}
	if !strings.Contains(string(data), crashMarker) {
		t.Fatalf("panic 堆栈未写入日志文件(%s)，子进程输出:\n%s", logPath, out)
	}
	// 堆栈里应有 goroutine 帧，确认是完整的崩溃转储而不是普通日志行
	if !strings.Contains(string(data), "panic:") || !strings.Contains(string(data), "goroutine") {
		t.Fatalf("日志文件里的崩溃转储不完整:\n%s", data)
	}
}

// 未配置日志文件（输出到标准输出）时，不要额外接管崩溃输出（交给启动方式处理）。
func TestCrashOutputDisabledWithoutLogFile(t *testing.T) {
	SetupLogger(LogConfig{Enabled: true, File: ""})
	t.Cleanup(func() { SetupLogger(LogConfig{Enabled: false}) })
}

// 热重载会反复调 SetupLogger（每次都需要重开崩溃输出 sink），必须关掉上一个句柄，
// 否则每保存一次配置就泄漏一个 fd。
func TestSetupLoggerDoesNotLeakFds(t *testing.T) {
	before, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skip("无 /proc/self/fd，跳过 fd 计数")
	}
	logPath := filepath.Join(t.TempDir(), "x.log")
	for i := 0; i < 20; i++ {
		SetupLogger(LogConfig{Enabled: true, File: logPath})
	}
	t.Cleanup(func() { SetupLogger(LogConfig{Enabled: false}) })
	after, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skip("无 /proc/self/fd，跳过 fd 计数")
	}
	if len(after) > len(before)+3 { // ReadDir 自身占用的 fd 与调度噪声留 3 个余量
		t.Fatalf("反复配置日志后 fd 泄漏: before=%d after=%d", len(before), len(after))
	}
}
