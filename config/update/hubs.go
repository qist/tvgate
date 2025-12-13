package update

import (
	"time"
	
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/stream"
)

// UpdateHubsOnConfigChange 根据配置变更更新Hubs
// 配置变更时调用
func UpdateHubsOnConfigChange(newIfaces []string) {
	config.CfgMu.RLock()
	newRejoinInterval := config.Cfg.Server.McastRejoinInterval
	config.CfgMu.RUnlock()
	
	for oldKey, hub := range stream.GlobalMultiChannelHub.Hubs {
		// 更新多播重新加入间隔
		hub.Mu.Lock()
		oldRejoinInterval := hub.RejoinInterval
		hub.RejoinInterval = newRejoinInterval
		
		// 如果定时器存在，先停止它
		if hub.RejoinTimer != nil {
			hub.RejoinTimer.Stop()
		}
		
		// 如果新的间隔大于0，则重新启动定时器
		if newRejoinInterval > 0 {
			hub.RejoinTimer = time.NewTimer(newRejoinInterval)
			go func(h *stream.StreamHub, interval time.Duration) {
				for {
					select {
					case <-h.RejoinTimer.C:
						h.RejoinMulticastGroups(h.AddrList)
						// 重置定时器
						h.Mu.Lock()
						if h.RejoinTimer != nil {
							h.RejoinTimer.Reset(interval)
						}
						h.Mu.Unlock()
					}
				}
			}(hub, newRejoinInterval)
		} else {
			// 如果新的间隔为0，则清除定时器
			hub.RejoinTimer = nil
		}
		hub.Mu.Unlock()
		
		// 记录日志（如果有变更）
		if oldRejoinInterval != newRejoinInterval {
			logger.LogPrintf("🔄 更新 Hub %s 的多播重新加入间隔: %v -> %v", 
				oldKey, oldRejoinInterval, newRejoinInterval)
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
}