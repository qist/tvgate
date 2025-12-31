package update

import (
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/stream"
)

// UpdateHubsOnConfigChange 根据配置变更更新Hubs
// 配置变更时调用
func UpdateHubsOnConfigChange(newIfaces []string) {
	config.CfgMu.RLock()
	newRejoinInterval := config.Cfg.Server.McastRejoinInterval
	// 获取FCC相关配置
	newFccTypeStr := config.Cfg.Server.FccType
	newFccCacheSize := config.Cfg.Server.FccCacheSize
	newFccPortMin := config.Cfg.Server.FccListenPortMin
	newFccPortMax := config.Cfg.Server.FccListenPortMax
	
	config.CfgMu.RUnlock()
	
	for oldKey, hub := range stream.GlobalMultiChannelHub.Hubs {
		// 更新多播重新加入间隔
		oldRejoinInterval := hub.GetRejoinInterval()
		hub.SetRejoinInterval(newRejoinInterval)
		hub.UpdateRejoinTimer()
		
		// 记录日志（如果有变更）
		if oldRejoinInterval != newRejoinInterval {
			logger.LogPrintf("🔄 更新 Hub %s 的多播重新加入间隔: %v -> %v", 
				oldKey, oldRejoinInterval, newRejoinInterval)
		}
		
		// 更新FCC配置
		oldFccType := hub.GetFccType()
		oldFccCacheSize := hub.GetFccCacheSize()
		oldFccPortMin := hub.GetFccPortMin()
		oldFccPortMax := hub.GetFccPortMax()
		
		// 确定FCC类型
		newFccType := "telecom" // 默认为电信类型
		switch newFccTypeStr {
		case "huawei":
			newFccType = "huawei"
		case "telecom":
			newFccType = "telecom"
		}
		
		hub.SetFccType(newFccType)
		hub.SetFccParams(newFccCacheSize, newFccPortMin, newFccPortMax)
		
		// 记录FCC配置变更日志
		if oldFccType != newFccType {
			logger.LogPrintf("🔄 更新 Hub %s 的FCC类型: %v -> %v", 
				oldKey, oldFccType, newFccType)
		}
		if oldFccCacheSize != newFccCacheSize {
			logger.LogPrintf("🔄 更新 Hub %s 的FCC缓存大小: %v -> %v", 
				oldKey, oldFccCacheSize, newFccCacheSize)
		}
		if oldFccPortMin != newFccPortMin {
			logger.LogPrintf("🔄 更新 Hub %s 的FCC监听端口最小值: %v -> %v", 
				oldKey, oldFccPortMin, newFccPortMin)
		}
		if oldFccPortMax != newFccPortMax {
			logger.LogPrintf("🔄 更新 Hub %s 的FCC监听端口最大值: %v -> %v", 
				oldKey, oldFccPortMax, newFccPortMax)
		}
		
		// 生成新 key
		newKey := stream.GlobalMultiChannelHub.HubKey(hub.AddrList[0],newIfaces)

		if oldKey == newKey {
			// key 没变，只更新接口
			_ = hub.UpdateInterfaces(newIfaces)
			continue
		}

		// 创建新 Hub
		newHub, err := stream.NewStreamHub(hub.AddrList, newIfaces)
		if err != nil {
			logger.LogPrintf("❌ 新 Hub 创建失败: %v", err)
			continue
		}

		// 客户端迁移
		hub.TransferClientsTo(newHub)

		// 替换到 GlobalMultiChannelHub
		stream.GlobalMultiChannelHub.Mu.Lock()
		delete(stream.GlobalMultiChannelHub.Hubs, oldKey)
		stream.GlobalMultiChannelHub.Hubs[newKey] = newHub
		stream.GlobalMultiChannelHub.Mu.Unlock()
	}
	
	// 调用新增的接口，更新所有StreamHubs的配置
	stream.UpdateAllHubsConfig(nil)
}