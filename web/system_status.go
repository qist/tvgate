package web

import (
	"fmt"
	"time"

	"github.com/qist/tvgate/config"
)

// formatUptime 格式化服务器运行时间，显示为"天 小时 分 秒"格式，仅显示大于0的部分
func formatUptime(d time.Duration) string {
	if d <= 0 {
		return "0秒"
	}

	// 修复：确保时间在合理范围内，避免显示异常大的数值
	totalSeconds := int64(d.Seconds())
	if totalSeconds > 365*24*3600 { // 最多显示一年
		// 返回条件显示格式，而不是固定格式
		return "365天"
	}

	days := totalSeconds / 86400
	hours := (totalSeconds % 86400) / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60

	// 仅显示大于0的部分，与monitor页面保持一致
	result := ""
	if days > 0 {
		result += fmt.Sprintf("%d天", days)
	}
	if hours > 0 {
		result += fmt.Sprintf("%d小时", hours)
	}
	if minutes > 0 {
		result += fmt.Sprintf("%d分", minutes)
	}
	result += fmt.Sprintf("%d秒", seconds)

	return result
}

// getSystemUptime 获取系统运行时间
func getSystemUptime() string {
	uptime := time.Since(config.StartTime)
	// 如果时间差为0或负数，则返回默认值
	if uptime <= 0 {
		return "0秒"
	}

	// 修复：检查时间是否合理，避免系统时间错误导致的异常值
	if uptime > 365*24*time.Hour { // 最多显示一年
		uptime = 365 * 24 * time.Hour
	}

	return formatUptime(uptime)
}
