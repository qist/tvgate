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

func LogPrintf(format string, v ...interface{}) {
	logger.RLock()
	defer logger.RUnlock()
	if logger.enabled && logger.output != nil {
		fmt.Fprintf(logger.output, time.Now().Format("2006/01/02 15:04:05 ")+format+"\n", v...)
	}
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
