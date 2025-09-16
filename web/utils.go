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
	case ms > 0:
		if ms < 1000 {
			return fmt.Sprintf("%dms", ms)
		}
		return fmt.Sprintf("%ds", s)
	default:
		return fmt.Sprintf("%ds", s)
	}
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

	default:
		return "", fmt.Errorf("unsupported duration type: %T", value)
	}

	if d <= 0 {
		return "", nil
	}

	// 格式化为精简形式（支持 ms）
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	ms := int(d.Milliseconds()) % 1000

	switch {
	case h > 0:
		if m == 0 && s == 0 {
			return fmt.Sprintf("%dh", h), nil
		} else if s == 0 {
			return fmt.Sprintf("%dh%dm", h, m), nil
		} else {
			return fmt.Sprintf("%dh%dm%ds", h, m, s), nil
		}
	case m > 0:
		if s == 0 {
			return fmt.Sprintf("%dm", m), nil
		}
		return fmt.Sprintf("%dm%ds", m, s), nil
	case s > 0:
		if ms > 0 {
			return fmt.Sprintf("%ds%dms", s, ms), nil
		}
		return fmt.Sprintf("%ds", s), nil
	default:
		return fmt.Sprintf("%dms", ms), nil
	}
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
