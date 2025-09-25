package update

import (
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/stream"
)

// UpdateHubsOnConfigChange 根据配置变更更新Hubs
// 配置变更时调用
func UpdateHubsOnConfigChange(newIfaces []string) {
	for oldKey, hub := range stream.GlobalMultiChannelHub.Hubs {
		// 生成新 key
		newKey := stream.GlobalMultiChannelHub.HubKey(hub.AddrList[0])

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
}
