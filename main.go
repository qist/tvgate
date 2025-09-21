package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/cloudflare/tableflip"
	"github.com/qist/tvgate/auth"
	// "github.com/qist/tvgate/clear"
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
	httpclient "github.com/qist/tvgate/utils/http"
	"github.com/qist/tvgate/web"
	_ "net/http/pprof"
)

var shutdownMux sync.Mutex
var shutdownOnce sync.Once

func main() {
	flag.Parse()

	// 启动 pprof 性能分析接口（默认 6060 端口）
	go func() {
		log.Println("pprof 性能分析接口已启动: http://0.0.0.0:6060/debug/pprof/ 可远程访问")
		http.ListenAndServe("0.0.0.0:6060", nil)
	}()
	if *config.VersionFlag {
		fmt.Println("程序版本:", config.Version)
		return
	}

	// -------------------------
	// 初始化 tableflip Upgrader（仅非 Windows 平台）
	// -------------------------
	var upg *tableflip.Upgrader
	var err error
	isWindows := runtime.GOOS == "windows"
	if !isWindows {
		upg, err = tableflip.New(tableflip.Options{})
		if err != nil {
			log.Fatalf("无法创建升级器: %v", err)
		}
		defer upg.Stop() // 确保退出时清理
	} else {
		upg = nil
		// fmt.Println("Windows 平台不支持 tableflip 热升级，采用普通重启")
	}

	// -------------------------
	// 配置文件加载
	// -------------------------
	userConfigPath := *config.ConfigFilePath
	configFilePath, err := web.EnsureConfigFile(userConfigPath)
	if err != nil {
		log.Fatalf("确保配置文件失败: %v", err)
	}
	*config.ConfigFilePath = configFilePath
	fmt.Println("使用配置文件:", configFilePath)

	if err := load.LoadConfig(configFilePath); err != nil {
		log.Fatalf("加载配置文件失败: %v", err)
	}
	config.Cfg.SetDefaults()

	client := httpclient.NewHTTPClient(&config.Cfg, nil)

	// -------------------------
	// 初始化代理组统计
	// -------------------------
	groupstats.InitProxyGroups()
	for _, group := range config.Cfg.ProxyGroups {
		group.Stats = &config.GroupStats{
			ProxyStats: make(map[string]*config.ProxyStats),
		}
	}

	// -------------------------
	// 初始化全局 token 管理器
	// -------------------------
	if config.Cfg.GlobalAuth.TokensEnabled {
		auth.GlobalTokenManager = auth.NewGlobalTokenManagerFromConfig(&config.Cfg.GlobalAuth)
	} else {
		auth.GlobalTokenManager = nil
	}
	// tm := &auth.TokenManager{
	// 	Enabled:       true,
	// 	StaticTokens:  make(map[string]*auth.SessionInfo),
	// 	DynamicTokens: make(map[string]*auth.SessionInfo),
	// }
	// go func() {
	// 	ticker := time.NewTicker(time.Minute)
	// 	defer ticker.Stop()
	// 	for range ticker.C {
	// 		tm.CleanupExpiredSessions()
	// 	}
	// }()

	// -------------------------
	// 启动监控 & 清理任务
	// -------------------------
	// go monitor.ActiveClients.StartCleaner(30*time.Second, 20*time.Second)
	go monitor.StartSystemStatsUpdater(10 * time.Second)

	stopCleaner := make(chan struct{})
	stopAccessCleaner := make(chan struct{})
	stopProxyStats := make(chan struct{})

	// go clear.StartRedirectChainCleaner(10*time.Minute, 30*time.Minute, stopCleaner)
	// go clear.StartAccessCacheCleaner(10*time.Minute, 30*time.Minute, stopAccessCleaner)
	// go clear.StartGlobalProxyStatsCleaner(10*time.Minute, 2*time.Hour, stopProxyStats)

	// -------------------------
	// 日志
	// -------------------------
	logger.SetupLogger(logger.LogConfig{
		Enabled:    config.Cfg.Log.Enabled,
		File:       config.Cfg.Log.File,
		MaxSizeMB:  config.Cfg.Log.MaxSizeMB,
		MaxBackups: config.Cfg.Log.MaxBackups,
		MaxAgeDays: config.Cfg.Log.MaxAgeDays,
		Compress:   config.Cfg.Log.Compress,
	})

	// -------------------------
	// HTTP 路由
	// -------------------------
	jxHandler := jx.NewJXHandler(&config.Cfg.JX)
	mux := http.NewServeMux()
	go watch.WatchConfigFile(*config.ConfigFilePath)

	monitorPath := config.Cfg.Monitor.Path
	if monitorPath == "" {
		monitorPath = "/status"
	}
	mux.Handle(monitorPath, server.SecurityHeaders(http.HandlerFunc(monitor.HandleMonitor)))

	jxPath := config.Cfg.JX.Path
	if jxPath == "" {
		jxPath = "/jx"
	}
	mux.Handle(jxPath, server.SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jxHandler.Handle(w, r)
	})))

	if config.Cfg.Web.Enabled {
		webConfig := web.WebConfig{
			Username: config.Cfg.Web.Username,
			Password: config.Cfg.Web.Password,
			Enabled:  config.Cfg.Web.Enabled,
			Path:     config.Cfg.Web.Path,
		}
		configHandler := web.NewConfigHandler(webConfig)
		configHandler.RegisterRoutes(mux)
	}

	defaultHandler := server.SecurityHeaders(http.HandlerFunc(h.Handler(client)))

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

	// -------------------------
	// context 管理
	// -------------------------
	config.ServerCtx, config.Cancel = context.WithCancel(context.Background())

	// -------------------------
	// 启动 HTTP Server（支持 tableflip 热更）
	// -------------------------
	go func() {
		if err := server.StartHTTPServer(config.ServerCtx, mux, upg); err != nil {
			log.Fatalf("启动HTTP服务器失败: %v", err)
		}
	}()

	// -------------------------
	// 捕获系统退出信号
	// -------------------------
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		fmt.Println("收到退出信号，开始优雅退出")
		gracefulShutdown(stopCleaner, stopAccessCleaner, stopProxyStats)
		if !isWindows && upg != nil {
			upg.Exit() // tableflip 清理旧进程
		} else {
			os.Exit(0)
		}
	}()

	// -------------------------
	// tableflip 准备完成（仅非 Windows）
	// -------------------------
	if !isWindows && upg != nil {
		if err := upg.Ready(); err != nil {
			log.Fatalf("升级器准备失败: %v", err)
		}
	}

	<-config.ServerCtx.Done()
	gracefulShutdown(stopCleaner, stopAccessCleaner, stopProxyStats)
}

func gracefulShutdown(stopCleaner, stopAccessCleaner, stopProxyStats chan struct{}) {
	shutdownOnce.Do(func() {
		shutdownMux.Lock()
		defer shutdownMux.Unlock()

		if config.Cancel != nil {
			config.Cancel()
		}

		close(stopCleaner)
		close(stopAccessCleaner)
		close(stopProxyStats)

		fmt.Println("优雅退出完成")
	})
}
