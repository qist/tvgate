package load

import (
	"fmt"
	"os"
	// "strings"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/groupstats"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/player"
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

	// 必须先补齐默认值再发布全局配置：HTTP 等子结构含 *bool 字段（insecure_skip_verify /
	// disable_keepalives …），YAML 没写这些键时 Unmarshal 出来就是 nil。若先发布后补默认值，
	// 发布瞬间到调用方 SetDefaults 之间任何并发读取（新建 HTTP client、DNS 解析等）都会空指针
	// panic 直接带走进程 —— 实测：后台保存配置 → 本函数发布 nil 配置 → 出口通知让播放器
	// 重载 → 其 EPG 拉取 goroutine 走 NewHTTPClient 解引用 nil 后进程崩溃。
	// 而 web 各保存路径（saveAndResponse）之后并**不会**再调 SetDefaults，nil 会一直留着，
	// 后续任何一次 HTTP client 创建 / DNS 解析都会再崩一次。SetDefaults 幂等，这里提前调用安全。
	newCfg.SetDefaults()
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

	// 配置加载的**统一出口**通知播放器：player 段真的变了就立即重载订阅（台标/EPG/订阅源
	// 等改动即时生效）。放在这里而不是各个保存/监听调用点，是因为加载路径有十几处
	// （后台各配置页保存、备份恢复、文件监听…），漏掉任何一处都会让改动"要重启才生效"。
	// 通知内部与"已生效的配置"对比，重复调用是空转。
	player.NotifyPlayerConfigChanged(newCfg.Player)
	return nil
}
