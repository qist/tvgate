package update

import (
	"strings"

	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/stream"
)

// UpdateHubsOnConfigChange 根据配置变更更新Hubs
func UpdateHubsOnConfigChange(newIfaces []string) {
	// 遍历所有Hub并更新它们的网络接口
	stream.GlobalMultiChannelHub.Mu.RLock()
	var pairs []struct {
		key    string
		hub    *stream.StreamHub
		newKey string
	}

	for key, hub := range stream.GlobalMultiChannelHub.Hubs {
		parts := strings.SplitN(key, "|", 2)
		addr := parts[0]
		newKey := stream.GlobalMultiChannelHub.HubKey(addr)
		if key == newKey {
			continue
		}
		pairs = append(pairs, struct {
			key    string
			hub    *stream.StreamHub
			newKey string
		}{key, hub, newKey})
	}
	stream.GlobalMultiChannelHub.Mu.RUnlock()

	for _, p := range pairs {
		logger.LogPrintf("♻️ 零丢包更新组播监听：%s → %s", p.key, p.newKey)

		// 直接在旧Hub上更新网络接口
		if err := p.hub.UpdateInterfaces(newIfaces); err != nil {
			logger.LogPrintf("❌ 更新网络接口失败: %v", err)
			continue
		}

		// 更新成功，更新MultiChannelHub映射中的键
		stream.GlobalMultiChannelHub.Mu.Lock()
		delete(stream.GlobalMultiChannelHub.Hubs, p.key)
		stream.GlobalMultiChannelHub.Hubs[p.newKey] = p.hub
		stream.GlobalMultiChannelHub.Mu.Unlock()

		logger.LogPrintf("✅ 成功更新网络接口: %s", p.newKey)
	}
}
