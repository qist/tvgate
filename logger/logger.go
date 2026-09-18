// logger/logger.go
package logger

import (
	"fmt"
	"gopkg.in/natefinch/lumberjack.v2"
	"io"
	"log"
	"os"
	"runtime/debug"
	"sync"
	"time"
)

type LogConfig struct {
	Enabled    bool
	File       string
	MaxSizeMB  int
	MaxBackups int
	MaxAgeDays int
	Compress   bool
}

var logger = struct {
	sync.RWMutex
	enabled bool
	output  io.Writer
	// sinkFile 是给 debug.SetCrashOutput / 标准库 log 用的日志文件句柄（OpenFile 打开）。
	// SetupLogger 每次都会重新打开（热重载会重复调用），这里记住上一个并在替换时关掉，
	// 否则每次配置重载泄漏一个 fd。
	sinkFile *os.File
}{
	enabled: false,
	output:  io.Discard,
}

const logBufferMaxLines = 500

type logBroker struct {
	sync.Mutex
	subs   map[int]chan string
	nextID int
	buffer []string
}

func newLogBroker() *logBroker {
	return &logBroker{
		subs:   make(map[int]chan string),
		buffer: make([]string, 0, logBufferMaxLines),
	}
}

func (b *logBroker) append(line string) {
	b.Lock()
	if len(b.buffer) >= logBufferMaxLines {
		copy(b.buffer, b.buffer[1:])
		b.buffer[logBufferMaxLines-1] = line
	} else {
		b.buffer = append(b.buffer, line)
	}
	for _, ch := range b.subs {
		select {
		case ch <- line:
		default:
		}
	}
	b.Unlock()
}

func (b *logBroker) snapshot() []string {
	b.Lock()
	defer b.Unlock()
	out := make([]string, len(b.buffer))
	copy(out, b.buffer)
	return out
}

func (b *logBroker) subscribe() (<-chan string, func()) {
	b.Lock()
	id := b.nextID
	b.nextID++
	ch := make(chan string, 200)
	b.subs[id] = ch
	b.Unlock()

	cancel := func() {
		b.Lock()
		if sub, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(sub)
		}
		b.Unlock()
	}
	return ch, cancel
}

var broker = newLogBroker()

func LogPrintf(format string, v ...interface{}) {
	logger.RLock()
	enabled := logger.enabled
	out := logger.output
	logger.RUnlock()

	if !enabled || out == nil {
		return
	}

	line := time.Now().Format("2006/01/02 15:04:05 ") + fmt.Sprintf(format, v...)
	fmt.Fprintln(out, line)
	broker.append(line)
}

// IsEnabled 返回日志是否启用，用于热路径避免不必要的参数求值
func IsEnabled() bool {
	logger.RLock()
	defer logger.RUnlock()
	return logger.enabled
}

func SetupLogger(cfg LogConfig) {
	logger.Lock()
	defer logger.Unlock()

	// 上一个 sink 句柄交给下面替换时统一关闭（debug.SetCrashOutput 内部已复制 fd，
	// 关掉我们这份不影响它的崩溃输出）
	prevSink := logger.sinkFile
	logger.sinkFile = nil
	defer func() {
		if prevSink != nil {
			prevSink.Close()
		}
	}()

	if !cfg.Enabled {
		logger.enabled = false
		logger.output = io.Discard
		_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
		return
	}

	logger.enabled = true
	if cfg.File == "" {
		logger.output = os.Stdout
		// 未配置日志文件（标准输出）：崩溃输出留在 stderr，由启动方式决定去哪
		_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
		return
	}
	logger.output = &lumberjack.Logger{
		Filename:   cfg.File,
		MaxSize:    cfg.MaxSizeMB,
		MaxBackups: cfg.MaxBackups,
		MaxAge:     cfg.MaxAgeDays,
		Compress:   cfg.Compress,
	}
	// 崩溃输出也写进日志文件：panic 堆栈 / fatal error 走的是 fd 2（stderr），只靠启动脚本的
	// 2>&1 才看得到 —— systemd、后台面板、容器启动都会丢，而"进程莫名消失"时这条堆栈恰恰是
	// 唯一线索（实测：空指针 panic 只出现在 shell 重定向里）。debug.SetCrashOutput 会另开一份
	// 复制 fd 写该文件，与启动方式无关；重复调用覆盖旧目标，不会泄漏 fd。
	if f, err := os.OpenFile(cfg.File, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
			f.Close()
		} else {
			// 标准库 log（如 net/http 的 "panic serving ..."、"http: Server closed"）同样并入日志文件；
			// stderr 与日志文件本就是同一个文件时（启动脚本 2>&1 到它）不再写第二遍，避免重复行。
			writers := []io.Writer{f}
			if differentFiles(os.Stderr, f) {
				writers = append(writers, os.Stderr)
			}
			log.SetOutput(io.MultiWriter(writers...))
			logger.sinkFile = f
		}
	}
}

// differentFiles 判断两个已打开的文件是否不是同一个文件（管道/终端也算不同）。
// 用于避免"stderr 已被启动脚本重定向到日志文件"时再写一遍导致重复行。
func differentFiles(a, b *os.File) bool {
	ai, err := a.Stat()
	if err != nil {
		return true
	}
	bi, err := b.Stat()
	if err != nil {
		return true
	}
	return !os.SameFile(ai, bi)
}

// GetBufferSnapshot returns the latest log lines kept in memory.
func GetBufferSnapshot() []string {
	return broker.snapshot()
}

// Subscribe returns a channel for live log lines and a cancel function.
func Subscribe() (<-chan string, func()) {
	return broker.subscribe()
}
