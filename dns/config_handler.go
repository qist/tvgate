package dns

import (
	"github.com/qist/tvgate/config"
)

// HandleConfigUpdate 处理配置更新事件
func HandleConfigUpdate(oldCfg, newCfg *config.Config) {
	// 检查DNS配置是否发生变化
	if isDNSConfigChanged(oldCfg, newCfg) {
		// 刷新DNS配置
		GetInstance().RefreshConfig()
	}
}

// isDNSConfigChanged 检查DNS配置是否发生变化
func isDNSConfigChanged(oldCfg, newCfg *config.Config) bool {
	// 检查DNS服务器列表是否发生变化
	if len(oldCfg.DNS.Servers) != len(newCfg.DNS.Servers) {
		return true
	}
	
	for i, server := range oldCfg.DNS.Servers {
		if server != newCfg.DNS.Servers[i] {
			return true
		}
	}
	
	// 检查超时配置是否发生变化
	if oldCfg.DNS.Timeout != newCfg.DNS.Timeout {
		return true
	}
	
	// 检查最大连接数是否发生变化
	if oldCfg.DNS.MaxConns != newCfg.DNS.MaxConns {
		return true
	}
	
	return false
}