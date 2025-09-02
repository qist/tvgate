package update

import (
	"strings"
	"time"

	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/stream"
)

// 平滑更新：新 Hub 预热 -> 客户端迁移 -> 关闭旧 Hub
func UpdateHubsOnConfigChange(newIfaces []string) {
	stream.HubsMu.Lock()
	var pairs []struct {
		oldKey string
		oldHub *stream.StreamHub
		addr   string
		newKey string
	}
	for key, hub := range stream.Hubs {
		parts := strings.SplitN(key, "|", 2)
		addr := parts[0]
		newKey := stream.HubKey(addr, newIfaces)
		if key == newKey {
			continue
		}
		pairs = append(pairs, struct {
			oldKey string
			oldHub *stream.StreamHub
			addr   string
			newKey string
		}{key, hub, addr, newKey})
	}
	stream.HubsMu.Unlock()

	for _, p := range pairs {
		logger.LogPrintf("♻️ 零丢包更新组播监听：%s → %s", p.oldKey, p.newKey)
		newHub, err := stream.NewStreamHub(p.addr, newIfaces)
		if err != nil {
			logger.LogPrintf("❌ 创建新 Hub 失败: %v", err)
			continue
		}
		// 预热：等待新 Hub 收到第一帧（可按需调整/替换为条件变量）
		time.Sleep(500 * time.Millisecond)

		// 迁移客户端
		p.oldHub.TransferClientsTo(newHub)

		// 替换注册
		stream.HubsMu.Lock()
		delete(stream.Hubs, p.oldKey)
		stream.Hubs[p.newKey] = newHub
		stream.HubsMu.Unlock()

		// 延迟关闭旧 Hub
		go func(oldKey string, oldHub *stream.StreamHub) {
			time.Sleep(5 * time.Second)
			oldHub.Close()
			logger.LogPrintf("🛑 已关闭旧 Hub：%s", oldKey)
		}(p.oldKey, p.oldHub)
	}
}
