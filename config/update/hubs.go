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
	newRejoinInterval := config.Cfg.Multicast.McastRejoinInterval
	newFccTypeStr := config.Cfg.Multicast.FccType
	newFccCacheSize := config.Cfg.Multicast.FccCacheSize
	newFccPortMin := config.Cfg.Multicast.FccListenPortMin
	newFccPortMax := config.Cfg.Multicast.FccListenPortMax

	config.CfgMu.RUnlock()

	// 先获取所有 Hub 的快照，避免在迭代过程中修改 Map 导致的竞态或逻辑错误
	stream.GlobalMultiChannelHub.Mu.RLock()
	hubsSnapshot := make(map[string]*stream.StreamHub)
	for k, v := range stream.GlobalMultiChannelHub.Hubs {
		hubsSnapshot[k] = v
	}
	stream.GlobalMultiChannelHub.Mu.RUnlock()

	for oldKey, hub := range hubsSnapshot {
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
		newKey := stream.GlobalMultiChannelHub.HubKey(hub.AddrList[0], newIfaces)

		// 只要地址没变，直接在原 Hub 上更新接口，实现平滑迁移
		_ = hub.UpdateInterfaces(newIfaces)

		// 如果 Key 变了（网卡列表变了），更新 Map 中的 Key
		if oldKey != newKey {
			stream.GlobalMultiChannelHub.Mu.Lock()
			delete(stream.GlobalMultiChannelHub.Hubs, oldKey)
			stream.GlobalMultiChannelHub.Hubs[newKey] = hub
			stream.GlobalMultiChannelHub.Mu.Unlock()
			logger.LogPrintf("🔄 Hub Key 已更新: %s -> %s", oldKey, newKey)
		}

		continue
	}

	// 调用新增的接口，更新所有StreamHubs的配置
	stream.UpdateAllHubsConfig(nil)
}
