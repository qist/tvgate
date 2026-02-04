// logger/logger.go
package logger

import (
	"fmt"
	"gopkg.in/natefinch/lumberjack.v2"
	"io"
	"os"
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

func SetupLogger(cfg LogConfig) {
	logger.Lock()
	defer logger.Unlock()

	if !cfg.Enabled {
		logger.enabled = false
		logger.output = io.Discard
		return
	}

	logger.enabled = true
	if cfg.File == "" {
		logger.output = os.Stdout
	} else {
		logger.output = &lumberjack.Logger{
			Filename:   cfg.File,
			MaxSize:    cfg.MaxSizeMB,
			MaxBackups: cfg.MaxBackups,
			MaxAge:     cfg.MaxAgeDays,
			Compress:   cfg.Compress,
		}
	}
}

// GetBufferSnapshot returns the latest log lines kept in memory.
func GetBufferSnapshot() []string {
	return broker.snapshot()
}

// Subscribe returns a channel for live log lines and a cancel function.
func Subscribe() (<-chan string, func()) {
	return broker.subscribe()
}
