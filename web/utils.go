package web

import (
	"fmt"
	"time"
)

// formatDuration 格式化时间持续时间，如果为0则返回空字符串
func formatDuration(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return compactDuration(d)
}

// formatDurationString 格式化时间字符串，去掉多余的 0m0s
func formatDurationString(durationStr string) string {
	// 尝试解析为 Duration
	d, err := time.ParseDuration(durationStr)
	if err != nil {
		// 如果原字符串没有单位，当成秒
		if d2, err2 := time.ParseDuration(durationStr + "s"); err2 == nil {
			d = d2
		} else {
			return durationStr // 无法解析，原样返回
		}
	}

	return compactDuration(d)
}

func formatDurationValue(value interface{}) (string, error) {
	var d time.Duration

	switch v := value.(type) {
	case string:
		if v == "" {
			return "", fmt.Errorf("empty string")
		}
		// 尝试解析带单位的 duration
		parsed, err := time.ParseDuration(v)
		if err != nil {
			// 如果纯数字，则按秒解析
			var seconds int
			if _, err2 := fmt.Sscanf(v, "%d", &seconds); err2 == nil {
				d = time.Duration(seconds) * time.Second
			} else {
				return "", fmt.Errorf("invalid string duration: %v", v)
			}
		} else {
			d = parsed
		}

	case time.Duration:
		if v <= 0 {
			return "", fmt.Errorf("zero or negative duration")
		}
		d = v

	case float64:
		if v <= 0 {
			return "", fmt.Errorf("zero or negative duration")
		}
		d = time.Duration(v) * time.Second

	case int:
		if v <= 0 {
			return "", fmt.Errorf("zero or negative duration")
		}
		d = time.Duration(v) * time.Second

	default:
		return "", fmt.Errorf("unsupported duration type: %T", value)
	}

	if d <= 0 {
		return "", nil
	}

	return compactDuration(d), nil
}

// compactDuration 将 Duration 转换为 "1h"、"30m"、"45s"、"200ms" 这种简洁形式
func compactDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	ms := int(d.Milliseconds()) % 1000

	switch {
	case h > 0:
		if m == 0 && s == 0 {
			return fmt.Sprintf("%dh", h)
		} else if s == 0 {
			return fmt.Sprintf("%dh%dm", h, m)
		} else {
			return fmt.Sprintf("%dh%dm%ds", h, m, s)
		}
	case m > 0:
		if s == 0 {
			return fmt.Sprintf("%dm", m)
		}
		return fmt.Sprintf("%dm%ds", m, s)
	case s > 0:
		if ms > 0 {
			return fmt.Sprintf("%ds%dms", s, ms)
		}
		return fmt.Sprintf("%ds", s)
	default:
		return fmt.Sprintf("%dms", ms)
	}
}
