package update

import (
	"strings"
	"time"

	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/stream"
)

// UpdateHubsOnConfigChange 根据配置变更更新Hubs
func UpdateHubsOnConfigChange(newIfaces []string) {
	// logger.LogPrintf("✅ 配置文件重新加载完成")

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

		// 直接在旧Hub上更新网络接口，而不是创建新Hub
		if err := p.oldHub.UpdateInterfaces(p.addr, newIfaces); err != nil {
			logger.LogPrintf("❌ 更新网络接口失败: %v", err)
			
			// 如果更新失败，尝试创建新Hub并迁移客户端
			newHub, err := stream.NewStreamHub(p.addr, newIfaces)
			if err != nil {
				logger.LogPrintf("❌ 创建新 Hub 失败: %v", err)
				continue
			}

			// 预热：等待新 Hub 收到第一帧
			time.Sleep(500 * time.Millisecond)

			// 迁移客户端
			oldCount := len(p.oldHub.Clients)
			logger.LogPrintf("旧 Hub 客户端数量: %d", oldCount)
			p.oldHub.TransferClientsTo(newHub)
			newCount := len(newHub.Clients)
			logger.LogPrintf("新 Hub 客户端数量: %d", newCount)

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
		} else {
			// 更新成功，直接更新Hubs映射中的键
			stream.HubsMu.Lock()
			delete(stream.Hubs, p.oldKey)
			stream.Hubs[p.newKey] = p.oldHub
			stream.HubsMu.Unlock()
			logger.LogPrintf("✅ 成功更新网络接口: %s", p.newKey)
		}
	}
}