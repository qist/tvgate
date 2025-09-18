package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/qist/tvgate/auth"
	"github.com/qist/tvgate/clear"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/config/load"
	"github.com/qist/tvgate/config/watch"
	"github.com/qist/tvgate/domainmap"
	"github.com/qist/tvgate/groupstats"
	h "github.com/qist/tvgate/handler"
	"github.com/qist/tvgate/jx"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/monitor"
	"github.com/qist/tvgate/server"
	"github.com/qist/tvgate/stream"
	"github.com/qist/tvgate/utils/upgrade"
	httpclient "github.com/qist/tvgate/utils/http"
	"github.com/qist/tvgate/web"
)

var shutdownMux sync.Mutex

func main() {
	flag.Parse()
	if *config.VersionFlag {
		fmt.Println("程序版本:", config.Version)
		return
	}

	// 获取用户传入的 -config 参数
	userConfigPath := *config.ConfigFilePath

	// 确保配置文件存在
	configFilePath, err := web.EnsureConfigFile(userConfigPath)
	if err != nil {
		log.Fatalf("确保配置文件失败: %v", err)
	}
	*config.ConfigFilePath = configFilePath
	fmt.Println("使用配置文件:", configFilePath)

	// 加载配置
	if err := load.LoadConfig(configFilePath); err != nil {
		log.Fatalf("加载配置文件失败: %v", err)
	}
	config.Cfg.SetDefaults()

	// 初始化 HTTP client
	client := httpclient.NewHTTPClient(&config.Cfg, nil)

	// 初始化代理组统计信息
	groupstats.InitProxyGroups()
	for _, group := range config.Cfg.ProxyGroups {
		group.Stats = &config.GroupStats{
			ProxyStats: make(map[string]*config.ProxyStats),
		}
	}

	// 全局 token 管理器
	if config.Cfg.GlobalAuth.TokensEnabled {
		auth.GlobalTokenManager = auth.NewGlobalTokenManagerFromConfig(&config.Cfg.GlobalAuth)
	} else {
		auth.GlobalTokenManager = nil
	}

	// 定时清理 token
	tm := &auth.TokenManager{
		Enabled:       true,
		StaticTokens:  make(map[string]*auth.SessionInfo),
		DynamicTokens: make(map[string]*auth.SessionInfo),
	}
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			tm.CleanupExpiredSessions()
		}
	}()

	go monitor.ActiveClients.StartCleaner(30*time.Second, 20*time.Second)
	go monitor.StartSystemStatsUpdater(10 * time.Second)

	// 启动清理任务
	stopCleaner := make(chan struct{})
	go clear.StartRedirectChainCleaner(10*time.Minute, 30*time.Minute, stopCleaner)
	stopAccessCleaner := make(chan struct{})
	go clear.StartAccessCacheCleaner(10*time.Minute, 30*time.Minute, stopAccessCleaner)
	stopProxyStats := make(chan struct{})
	go clear.StartGlobalProxyStatsCleaner(10*time.Minute, 2*time.Hour, stopProxyStats)

	// 设置日志
	logger.SetupLogger(logger.LogConfig{
		Enabled:    config.Cfg.Log.Enabled,
		File:       config.Cfg.Log.File,
		MaxSizeMB:  config.Cfg.Log.MaxSizeMB,
		MaxBackups: config.Cfg.Log.MaxBackups,
		MaxAgeDays: config.Cfg.Log.MaxAgeDays,
		Compress:   config.Cfg.Log.Compress,
	})

	// 初始化 JX
	jxHandler := jx.NewJXHandler(&config.Cfg.JX)

	mux := http.NewServeMux()

	// 配置文件自动加载
	go watch.WatchConfigFile(*config.ConfigFilePath)

	// 监控路径
	monitorPath := config.Cfg.Monitor.Path
	if monitorPath == "" {
		monitorPath = "/status"
	}
	mux.Handle(monitorPath, server.SecurityHeaders(http.HandlerFunc(monitor.HandleMonitor)))

	// JX 路径
	jxPath := config.Cfg.JX.Path
	if jxPath == "" {
		jxPath = "/jx"
	}
	mux.Handle(jxPath, server.SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jxHandler.Handle(w, r)
	})))

	// Web 管理界面
	if config.Cfg.Web.Enabled {
		webConfig := web.WebConfig{
			Username: config.Cfg.Web.Username,
			Password: config.Cfg.Web.Password,
			Enabled:  config.Cfg.Web.Enabled,
			Path:     config.Cfg.Web.Path,
		}
		configHandler := web.NewConfigHandler(webConfig)
		configHandler.ServeMux(mux)
	}

	// 默认处理器
	defaultHandler := server.SecurityHeaders(http.HandlerFunc(h.Handler(client)))

	// 域名映射
	if len(config.Cfg.DomainMap) > 0 {
		mappings := make(auth.DomainMapList, len(config.Cfg.DomainMap))
		for i, mapping := range config.Cfg.DomainMap {
			mappings[i] = &auth.DomainMapConfig{
				Name:          mapping.Name,
				Source:        mapping.Source,
				Target:        mapping.Target,
				Protocol:      mapping.Protocol,
				Auth:          mapping.Auth,
				ClientHeaders: mapping.ClientHeaders,
				ServerHeaders: mapping.ServerHeaders,
			}
		}
		localClient := &http.Client{Timeout: config.Cfg.HTTP.Timeout}
		domainMapper := domainmap.NewDomainMapper(mappings, localClient, defaultHandler)
		mux.Handle("/", server.SecurityHeaders(domainMapper))
	} else {
		mux.Handle("/", defaultHandler)
	}

	config.ServerCtx, config.Cancel = context.WithCancel(context.Background())

	// 升级监听
	upgrade.StartUpgradeListener(func() {
		fmt.Println("收到升级通知，优雅退出...")

		// 关闭 StreamHub
		stream.HubsMu.Lock()
		for key, hub := range stream.Hubs {
			hub.Close()
			delete(stream.Hubs, key)
		}
		stream.HubsMu.Unlock()

		// 取消上下文
		if config.Cancel != nil {
			config.Cancel()
		}

		// 当前程序路径
		execPath, _ := os.Executable()
		tmpDir := filepath.Join(filepath.Dir(execPath), ".tmp_upgrade")

		args := []string{
			"-old=" + execPath,
			"-new=" + upgrade.NewExecPath(),
			"-config=" + *config.ConfigFilePath,
			"-tmp=" + tmpDir,
		}
		upgraderCmd := exec.Command(execPath, args...)
		upgraderCmd.Stdout = os.Stdout
		upgraderCmd.Stderr = os.Stderr
		if err := upgraderCmd.Start(); err != nil {
			log.Fatalf("启动升级子进程失败: %v", err)
		}
		fmt.Println("升级子进程启动, PID:", upgraderCmd.Process.Pid)
		os.Exit(0)
	})

	// 启动 HTTP Server
	go func() {
		if err := server.StartHTTPServer(config.ServerCtx, mux); err != nil {
			log.Fatalf("启动HTTP服务器失败: %v", err)
		}
	}()

	// 捕获系统信号
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		fmt.Println("收到退出信号，开始优雅退出")
		gracefulShutdown(stopCleaner, stopAccessCleaner, stopProxyStats)
	}()

	<-config.ServerCtx.Done()
	gracefulShutdown(stopCleaner, stopAccessCleaner, stopProxyStats)
}

func gracefulShutdown(stopCleaner, stopAccessCleaner, stopProxyStats chan struct{}) {
	shutdownMux.Lock()
	defer shutdownMux.Unlock()

	if config.Cancel != nil {
		config.Cancel()
	}
	close(stopCleaner)
	close(stopAccessCleaner)
	close(stopProxyStats)

	fmt.Println("优雅退出完成")
}
