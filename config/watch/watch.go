package watch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"github.com/cloudflare/tableflip"
	"github.com/fsnotify/fsnotify"

	"github.com/qist/tvgate/auth"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/config/load"
	"github.com/qist/tvgate/config/update"
	"github.com/qist/tvgate/dns"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/php"
	"github.com/qist/tvgate/player"
	"github.com/qist/tvgate/server"
	"github.com/qist/tvgate/stream"
	tvsync "github.com/qist/tvgate/sync"
	"github.com/qist/tvgate/tasks"
	tsync "github.com/qist/tvgate/utils/sync"
)

var watchWg tsync.WaitGroup

// WatchConfigFile 监控配置文件变更并平滑更新服务
func WatchConfigFile(ctx context.Context, configPath string, upgrader *tableflip.Upgrader) {
	if configPath == "" {
		return
	}

	absPath, err := filepath.Abs(configPath)
	if err != nil {
		logger.LogPrintf("❌ 获取配置文件绝对路径失败: %v", err)
		return
	}

	parentDir := filepath.Dir(absPath)
	if parentDir == "" {
		parentDir = "."
	}

	fileInfo, err := os.Stat(absPath)
	var lastModifiedTime time.Time
	if err == nil {
		lastModifiedTime = fileInfo.ModTime()
	} else {
		lastModifiedTime = time.Now()
		logger.LogPrintf("⚠️ 获取配置文件状态失败，将使用当前时间: %v", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logger.LogPrintf("❌ 创建文件监听失败: %v", err)
		return
	}
	defer watcher.Close()

	setupWatcher := func() error {
		if err := watcher.Add(parentDir); err != nil {
			return err
		}
		if err := watcher.Add(absPath); err != nil {
			return err
		}
		return nil
	}
	if err := setupWatcher(); err != nil {
		logger.LogPrintf("❌ 初始化文件监控失败: %v", err)
		return
	}

	var debounceTimer *time.Timer
	debounceDelay := time.Duration(config.Cfg.Reload) * time.Second

	var httpCancel context.CancelFunc
	var muxMu sync.Mutex

	// 缓存端口/证书状态，用于判断是否需要重启
	oldPort := config.Cfg.Server.Port
	oldHTTPPort := config.Cfg.Server.HTTPPort
	oldHTTPSPort := config.Cfg.Server.TLS.HTTPSPort
	oldCertFile := config.Cfg.Server.CertFile
	oldKeyFile := config.Cfg.Server.KeyFile
	oldTLSCertFile := config.Cfg.Server.TLS.CertFile
	oldTLSKeyFile := config.Cfg.Server.TLS.KeyFile

	reload := func() {
		info, err := os.Stat(configPath)
		if err != nil {
			logger.LogPrintf("❌ 获取文件信息失败: %v", err)
			return
		}
		if !info.ModTime().After(lastModifiedTime) {
			return
		}
		lastModifiedTime = info.ModTime()
		logger.LogPrintf("📦 检测到配置文件修改，准备重新加载...")
		// 记录重载前的 player 段，用于判断是否需要通知播放器立即重载订阅
		oldPlayerCfg := config.Cfg.Player

		if err := load.LoadConfig(configPath); err != nil {
			logger.LogPrintf("❌ 重新加载配置失败: %v", err)
			return
		}
		logger.LogPrintf("✅ 配置文件重新加载完成")
		// 🔹 这里刷新 DNS 实例
		dns.HandleConfigUpdate(&config.Config{}, &config.Cfg)
		config.CfgMu.RLock()
		update.UpdateHubsOnConfigChange(config.Cfg.Multicast.MulticastIfaces)
		// 设置默认值 & token 管理器
		config.Cfg.SetDefaults()
		// 🔹 重新初始化 PHP 模块（刷新 docroot、path 等配置）
		// 必须在 SetDefaults 之后：DocRoot 相对路径需先解析为绝对路径（相对配置文件所在目录），
		// 否则 php 模块拿到未解析的 "www"，热加载后脚本路径解析失败（404）。
		php.Init(&config.Cfg)
		// 更新TS缓存配置
		stream.InitOrUpdateTSCacheFromConfig()

		config.CfgMu.RUnlock()

		// 重启仓库同步（sync 配置变化时自动停止旧实例并按新配置启动）
		tvsync.Start(&config.Cfg)

		// 重启定时任务（tasks 配置变化时自动停止旧实例并按新配置重启调度）
		tasks.Start(&config.Cfg)

		// player 订阅/间隔等变化时立即重载并重置刷新计时
		// （否则新 update_interval 要等当前周期计时器到期才生效）
		if !reflect.DeepEqual(oldPlayerCfg, config.Cfg.Player) {
			player.NotifyConfigChanged()
		}

		muxMu.Lock()
		defer muxMu.Unlock()

		// 设置默认值 & token 管理器
		config.Cfg.SetDefaults()
		auth.ReloadGlobalTokenManager(&config.Cfg.GlobalAuth)
		auth.CleanupGlobalTokenManager()

		needRestart := oldPort != config.Cfg.Server.Port ||
			oldHTTPPort != config.Cfg.Server.HTTPPort ||
			oldHTTPSPort != config.Cfg.Server.TLS.HTTPSPort ||
			oldCertFile != config.Cfg.Server.CertFile ||
			oldKeyFile != config.Cfg.Server.KeyFile ||
			oldTLSCertFile != config.Cfg.Server.TLS.CertFile ||
			oldTLSKeyFile != config.Cfg.Server.TLS.KeyFile

		// 如果需要重启服务
		if needRestart {
			logger.LogPrintf("🔄 检测到关键配置变更，需要重启服务")

			// 先关闭旧服务
			if httpCancel != nil {
				logger.LogPrintf("🔄 正在通过上下文关闭旧服务...")
				httpCancel()
				// 等待服务完全关闭
				time.Sleep(500 * time.Millisecond)
			}

			// 直接关闭所有服务器
			logger.LogPrintf("🔄 正在直接关闭所有服务...")
			server.CloseAllServers()
			time.Sleep(100 * time.Millisecond)

			// 创建新的上下文
			serverCtx, cancel := context.WithCancel(ctx)
			httpCancel = cancel

			// 构建需要启动的新地址列表
			newAddrs := make(map[string]bool)
			newAddrs[fmt.Sprintf(":%d", config.Cfg.Server.Port)] = true
			if config.Cfg.Server.HTTPPort > 0 {
				newAddrs[fmt.Sprintf(":%d", config.Cfg.Server.HTTPPort)] = true
			}
			if config.Cfg.Server.TLS.HTTPSPort > 0 {
				newAddrs[fmt.Sprintf(":%d", config.Cfg.Server.TLS.HTTPSPort)] = true
			}

			// 启动所有新服务
			for addr := range newAddrs {
				server.RegisterMux(addr, &config.Cfg)
				logger.LogPrintf("🚀 正在启动服务 %s", addr)
				addr := addr // 局部变量捕获
				watchWg.Go(func() {
					if err := server.StartHTTPServerWithConfig(serverCtx, addr, nil, &config.Cfg); err != nil {
						logger.LogPrintf("❌ 启动 HTTP 服务失败 %s: %v", addr, err)
					}
				})
			}
		} else {
			// 平滑更新路由
			logger.LogPrintf("🔄 配置变更无需重启服务，进行平滑更新")

			// 构建地址列表
			addrs := make(map[string]bool)
			addrs[fmt.Sprintf(":%d", config.Cfg.Server.Port)] = true
			if config.Cfg.Server.HTTPPort > 0 {
				addrs[fmt.Sprintf(":%d", config.Cfg.Server.HTTPPort)] = true
			}
			if config.Cfg.Server.TLS.HTTPSPort > 0 {
				addrs[fmt.Sprintf(":%d", config.Cfg.Server.TLS.HTTPSPort)] = true
			}

			for addr := range addrs {
				mux := server.RegisterMux(addr, &config.Cfg)
				server.SetHTTPHandler(addr, mux)
			}
		}

		// 更新缓存
		oldPort = config.Cfg.Server.Port
		oldHTTPPort = config.Cfg.Server.HTTPPort
		oldHTTPSPort = config.Cfg.Server.TLS.HTTPSPort
		oldCertFile = config.Cfg.Server.CertFile
		oldKeyFile = config.Cfg.Server.KeyFile
		oldTLSCertFile = config.Cfg.Server.TLS.CertFile
		oldTLSKeyFile = config.Cfg.Server.TLS.KeyFile
	}

	for {
		select {
		case <-ctx.Done():
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			if httpCancel != nil {
				httpCancel()
			}
			watchWg.Wait()
			logger.LogPrintf("🛑 配置文件监控已停止")
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if filepath.Clean(event.Name) == filepath.Clean(absPath) {
				switch {
				case event.Op&(fsnotify.Write|fsnotify.Create) != 0:
					if debounceTimer != nil {
						debounceTimer.Stop()
					}
					debounceTimer = time.AfterFunc(debounceDelay, reload)
				case event.Op&(fsnotify.Rename|fsnotify.Remove) != 0:
					logger.LogPrintf("⚠️ 配置文件被重命名或删除，尝试重新建立监控")
					if debounceTimer != nil {
						debounceTimer.Stop()
					}
					time.Sleep(100 * time.Millisecond)
					if err := setupWatcher(); err == nil {
						debounceTimer = time.AfterFunc(debounceDelay, reload)
					}
				}
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			logger.LogPrintf("❌ 文件监听错误: %v", err)
			if err := setupWatcher(); err != nil {
				logger.LogPrintf("❌ 重新建立监控失败: %v", err)
			}
		}
	}
}
