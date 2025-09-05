package watch

import (
	"context"
	"github.com/fsnotify/fsnotify"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/config/load"
	"github.com/qist/tvgate/config/update"
	h "github.com/qist/tvgate/handler"
	"github.com/qist/tvgate/jx"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/monitor"
	"github.com/qist/tvgate/server"
	httpclient "github.com/qist/tvgate/utils/http"
	"github.com/qist/tvgate/web"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

func WatchConfigFile(configPath string) {
	var httpCancel context.CancelFunc
	var muxMu sync.Mutex
	if configPath == "" {
		return
	}

	// 获取配置文件的绝对路径
	absPath, err := filepath.Abs(configPath)
	if err != nil {
		logger.LogPrintf("❌ 获取配置文件绝对路径失败: %v", err)
		return
	}

	// 获取父目录路径
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
		logger.LogPrintf("⚠️ 获取配置文件状态失败，将使用当前时间作为基准: %v", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logger.LogPrintf("❌ 创建文件监听失败: %v", err)
		return
	}
	defer watcher.Close()

	// 添加监控
	setupWatcher := func() error {
		// 监控父目录
		if err := watcher.Add(parentDir); err != nil {
			logger.LogPrintf("⚠️ 添加父目录监听失败: %v", err)
			return err
		}

		// 监控配置文件本身
		if err := watcher.Add(absPath); err != nil {
			logger.LogPrintf("⚠️ 添加配置文件监听失败: %v", err)
			return err
		}

		logger.LogPrintf("✅ 成功设置配置文件监控: %s", absPath)
		return nil
	}

	if err := setupWatcher(); err != nil {
		logger.LogPrintf("❌ 初始化文件监控失败: %v", err)
		return
	}

	var debounceTimer *time.Timer
	debounceDelay := time.Duration(config.Cfg.Reload) * time.Second

	// 定期检查监控状态
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			if _, err := os.Stat(absPath); err != nil {
				logger.LogPrintf("⚠️ 配置文件状态异常，尝试重新建立监控: %v", err)
				setupWatcher()
			}
		}
	}()

	reload := func() {
		info, err := os.Stat(configPath)
		if err != nil {
			logger.LogPrintf("❌ 获取文件信息失败: %v", err)
			return
		}
		if info.ModTime().After(lastModifiedTime) {
			lastModifiedTime = info.ModTime()
			logger.LogPrintf("📦 检测到配置文件修改，准备重新加载...")

			if err := load.LoadConfig(configPath); err != nil {
				logger.LogPrintf("❌ 重新加载配置失败: %v", err)
				return
			}
			logger.LogPrintf("✅ 配置文件重新加载完成")
			// 平滑更新多播网卡监听（零丢包）
			config.CfgMu.RLock()
			update.UpdateHubsOnConfigChange(config.Cfg.Server.MulticastIfaces)
			config.CfgMu.RUnlock()
			// 添加监控路径处理
			// 平滑替换 HTTP 服务
			muxMu.Lock()
			defer muxMu.Unlock()

			if httpCancel != nil {
				httpCancel() // 关闭旧服务
			}
			// 2️⃣ 设置默认值
			config.Cfg.SetDefaults()
			jxHandler := jx.NewJXHandler(&config.Cfg.JX)
			newMux := http.NewServeMux()
			monitorPath := config.Cfg.Monitor.Path
			if monitorPath == "" {
				monitorPath = "/status"
			}
			client := httpclient.NewHTTPClient(&config.Cfg, nil)
			newMux.Handle(monitorPath, server.SecurityHeaders(http.HandlerFunc(monitor.Handler)))
			// jx 路径
			jxPath := config.Cfg.JX.Path
			if jxPath == "" {
				jxPath = "/jx"
			}
			newMux.Handle(jxPath, server.SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				jxHandler.Handle(w, r)
			})))
			// 注册 Web 管理界面处理器
			if config.Cfg.Web.Enabled {
				webConfig := web.WebConfig{
					Username: config.Cfg.Web.Username,
					Password: config.Cfg.Web.Password,
					Enabled:  config.Cfg.Web.Enabled,
					Path:     config.Cfg.Web.Path,
				}
				configHandler := web.NewConfigHandler(webConfig)
				configHandler.ServeMux(newMux)

				webHandler := web.NewWebHandler(configHandler)
				webHandler.ServeMux(newMux)
			}

			newMux.Handle("/", server.SecurityHeaders(http.HandlerFunc(h.Handler(client))))

			ctx, cancel := context.WithCancel(context.Background())
			httpCancel = cancel
			// 启动新 HTTP 服务（startHTTPServer 内部会处理平滑替换）
			go func() {
				defer func() {
					if r := recover(); r != nil {
						logger.LogPrintf("🔥 启动 HTTP 服务过程中发生 panic: %v", r)
					}
				}()
				if err := server.StartHTTPServer(ctx, newMux); err != nil && err != context.Canceled {
					logger.LogPrintf("❌ 启动 HTTP 服务失败: %v", err)
				}
			}()
		}
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			// 只关注配置文件的事件
			if filepath.Clean(event.Name) == filepath.Clean(absPath) {
				switch {
				case event.Op&(fsnotify.Write|fsnotify.Create) != 0:
					// 文件被修改或创建
					if debounceTimer != nil {
						debounceTimer.Stop()
					}
					debounceTimer = time.AfterFunc(debounceDelay, reload)

				case event.Op&(fsnotify.Rename|fsnotify.Remove) != 0:
					// 文件被重命名或删除
					logger.LogPrintf("⚠️ 检测到配置文件被重命名或删除，尝试重新建立监控")
					if debounceTimer != nil {
						debounceTimer.Stop()
					}
					// 等待一小段时间，让文件系统操作完成
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
			// 尝试重新建立监控
			if err := setupWatcher(); err != nil {
				logger.LogPrintf("❌ 重新建立监控失败: %v", err)
			}
		}
	}
}
