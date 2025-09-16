package web

import (
	"time"
	"fmt"
)

// formatDuration 格式化时间持续时间，如果为0则返回空字符串
func formatDuration(d time.Duration) string {
	if d <= 0 {
		return ""
	}

	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60

	// 只拼必要的部分
	if h > 0 && m == 0 && s == 0 {
		return fmt.Sprintf("%dh", h)
	}
	if h > 0 && s == 0 {
		return fmt.Sprintf("%dh%dm", h, m)
	}
	if h > 0 {
		return fmt.Sprintf("%dh%dm%ds", h, m, s)
	}
	if m > 0 && s == 0 {
		return fmt.Sprintf("%dm", m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// formatDurationString 格式化时间字符串，去掉多余的 0m0s
func formatDurationString(durationStr string) string {
    // 优先尝试解析为 Duration
    d, err := time.ParseDuration(durationStr)
    if err != nil {
        // 如果原字符串没有单位，当成秒
        if d2, err2 := time.ParseDuration(durationStr + "s"); err2 == nil {
            d = d2
        } else {
            return durationStr // 无法解析，原样返回
        }
    }

    // 按 h/m/s 重新拼接
    h := int(d.Hours())
    m := int(d.Minutes()) % 60
    s := int(d.Seconds()) % 60

    if h > 0 && m == 0 && s == 0 {
        return fmt.Sprintf("%dh", h)
    }
    if h > 0 && s == 0 {
        return fmt.Sprintf("%dh%dm", h, m)
    }
    if h > 0 {
        return fmt.Sprintf("%dh%dm%ds", h, m, s)
    }
    if m > 0 && s == 0 {
        return fmt.Sprintf("%dm", m)
    }
    if m > 0 {
        return fmt.Sprintf("%dm%ds", m, s)
    }
    return fmt.Sprintf("%ds", s)
}

func formatDurationValue(value interface{}) (string, error) {
	var d time.Duration
	switch v := value.(type) {
	case string:
		if v == "" {
			return "", fmt.Errorf("empty string")
		}
		// 直接尝试 ParseDuration（支持 1h、30m、45s、1h30m5s、1h0m0s）
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

	default:
		return "", fmt.Errorf("unsupported duration type: %T", value)
	}

	// 格式化为精简形式
	return compactDuration(d), nil
}

// compactDuration 将 Duration 转换为 "1h"、"30m"、"45s" 这种简洁形式
func compactDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60

	switch {
	case h > 0 && m == 0 && s == 0:
		return fmt.Sprintf("%dh", h)
	case h > 0 && s == 0:
		return fmt.Sprintf("%dh%dm", h, m)
	case h > 0:
		return fmt.Sprintf("%dh%dm%ds", h, m, s)
	case m > 0 && s == 0:
		return fmt.Sprintf("%dm", m)
	case m > 0:
		return fmt.Sprintf("%dm%ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}
