package web

import (
	"time"
)

// formatDuration 格式化时间持续时间，如果为0则返回空字符串
func formatDuration(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return d.String()
}

// formatDurationString 格式化时间字符串
func formatDurationString(durationStr string) string {
	// 如果已经包含时间单位，则直接返回
	if len(durationStr) > 0 {
		lastChar := durationStr[len(durationStr)-1:]
		if lastChar == "s" || lastChar == "m" || lastChar == "h" {
			return durationStr
		}
	}

	// 尝试解析为数字（秒）
	if seconds, err := time.ParseDuration(durationStr + "s"); err == nil {
		return seconds.String()
	}

	return durationStr
}