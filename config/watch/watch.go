package watch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cloudflare/tableflip"
	"github.com/fsnotify/fsnotify"

	"github.com/qist/tvgate/auth"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/config/load"
	"github.com/qist/tvgate/config/update"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/server"
)

// WatchConfigFile 监控配置文件变更并平滑更新服务
func WatchConfigFile(configPath string, upgrader *tableflip.Upgrader) {
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

		if err := load.LoadConfig(configPath); err != nil {
			logger.LogPrintf("❌ 重新加载配置失败: %v", err)
			return
		}
		logger.LogPrintf("✅ 配置文件重新加载完成")

		config.CfgMu.RLock()
		update.UpdateHubsOnConfigChange(config.Cfg.Server.MulticastIfaces)
		config.CfgMu.RUnlock()

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

		ports := []int{config.Cfg.Server.Port}
		if config.Cfg.Server.HTTPPort > 0 {
			ports = append(ports, config.Cfg.Server.HTTPPort)
		}
		if config.Cfg.Server.TLS.HTTPSPort > 0 {
			ports = append(ports, config.Cfg.Server.TLS.HTTPSPort)
		}

		for _, p := range ports {
			addr := fmt.Sprintf(":%d", p)
			mux := server.RegisterMux(addr, &config.Cfg)
			if needRestart {
				// 关闭旧服务
				if httpCancel != nil {
					httpCancel()
				}
				ctx, cancel := context.WithCancel(context.Background())
				httpCancel = cancel

				go func(addr string) {
					if err := server.StartHTTPServer(ctx, addr, nil); err != nil {
						logger.LogPrintf("❌ 启动 HTTP 服务失败 %s: %v", addr, err)
					}
				}(addr)
			} else {

				// 再平滑替换 Handler
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
