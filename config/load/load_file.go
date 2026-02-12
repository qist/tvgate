package load

import (
	"fmt"
	"os"
	// "strings"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/groupstats"
	"github.com/qist/tvgate/logger"
	"gopkg.in/yaml.v3"
)

func LoadConfig(configPath string) error {
	yamlData, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	var newCfg config.Config
	err = yaml.Unmarshal(yamlData, &newCfg)
	if err != nil {
		return err
	}

	// 配置有效性校验，避免 runtime panic
	if err := groupstats.ValidateConfig(newCfg.ProxyGroups); err != nil {
		return fmt.Errorf("配置校验失败: %w", err)
	}

	// trim iface names
	cleaned := make([]string, 0, len(config.Cfg.Multicast.MulticastIfaces))
	for _, n := range config.Cfg.Multicast.MulticastIfaces {
		if n != "" {
			cleaned = append(cleaned, n)
		}
	}
	config.Cfg.Multicast.MulticastIfaces = cleaned

	config.LogConfigMutex.Lock()
	defer config.LogConfigMutex.Unlock()

	// 合并原有运行状态（比如代理测速结果）
	groupstats.MergeProxyStats(config.Cfg.ProxyGroups, newCfg.ProxyGroups)
	config.Cfg = newCfg

	// 初始化统计结构
	groupstats.InitProxyGroups()

	// 打印基本加载信息
	logger.LogPrintf("✅ 配置文件已加载，代理组数量: %d", len(config.Cfg.ProxyGroups))
	for groupName, group := range config.Cfg.ProxyGroups {
		logger.LogPrintf("🔧 代理组: %s, 域名列表: %v", groupName, group.Domains)
	}

	logger.SetupLogger(logger.LogConfig{
		Enabled:    config.Cfg.Log.Enabled,
		File:       config.Cfg.Log.File,
		MaxSizeMB:  config.Cfg.Log.MaxSizeMB,
		MaxBackups: config.Cfg.Log.MaxBackups,
		MaxAgeDays: config.Cfg.Log.MaxAgeDays,
		Compress:   config.Cfg.Log.Compress,
	})
	return nil
}
