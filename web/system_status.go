package web

import (
	"time"
	"fmt"

	"github.com/qist/tvgate/config"
)

// formatUptime 格式化服务器运行时间，显示为"天 小时 分"格式
func formatUptime(d time.Duration) string {
	if d <= 0 {
		return "0天0小时0分"
	}

	totalSeconds := int64(d.Seconds())
	days := totalSeconds / 86400
	hours := (totalSeconds % 86400) / 3600
	minutes := (totalSeconds % 3600) / 60

	return fmt.Sprintf("%d天%d小时%d分", days, hours, minutes)
}

// getSystemUptime 获取系统运行时间
func getSystemUptime() string {
	uptime := time.Since(config.StartTime)
	// 添加调试信息
	// fmt.Printf("DEBUG: config.StartTime=%v, uptime=%v, formatted=%s\n", config.StartTime, uptime, formatUptime(uptime))
	return formatUptime(uptime)
}