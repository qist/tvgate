//go:build !android

// logger/timezone_other.go
// 非 Android 平台：time.Local 由系统 /etc/localtime 或 TZ 环境变量决定，无需特殊处理。
package logger
