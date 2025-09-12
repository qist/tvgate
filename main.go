package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
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
	httpclient "github.com/qist/tvgate/utils/http"
	"github.com/qist/tvgate/web"
)

func main() {
	flag.Parse()
	if *config.VersionFlag {
		fmt.Println("程序版本:", config.Version)
		return
	}
	configPath := *config.ConfigFilePath
	if configPath == "" {
		log.Fatal("未指定配置文件路径")
	}

	if err := load.LoadConfig(configPath); err != nil {
		log.Fatalf("加载配置文件失败: %v", err)
	}

	if *config.ConfigFilePath != "" {
		err := load.LoadConfig(*config.ConfigFilePath)
		if err != nil {
			log.Fatalf("读取YAML配置文件失败: %v", err)
		}
	}
	// 验证配置是否正确加载
	// if len(cfg.ProxyGroups) == 0 {
	// 	log.Fatal("警告: 未加载任何代理组配置")
	// }
	// 2️⃣ 设置默认值
	config.Cfg.SetDefaults()

	// 3️⃣ 初始化 HTTP client
	client := httpclient.NewHTTPClient(&config.Cfg, nil)
	// 初始化代理组统计信息
	groupstats.InitProxyGroups()

	// 初始化代理组统计信息
	for _, group := range config.Cfg.ProxyGroups {
		group.Stats = &config.GroupStats{
			ProxyStats: make(map[string]*config.ProxyStats),
		}
	}
	tm := &auth.TokenManager{
		Enabled:       true,
		StaticTokens:  make(map[string]*auth.SessionInfo),
		DynamicTokens: make(map[string]*auth.SessionInfo),
	}
	go func() {
		ticker := time.NewTicker(1 * time.Minute) // 每分钟清理一次
		defer ticker.Stop()
		for range ticker.C {
			tm.CleanupExpiredSessions()
		}
	}()
	go monitor.ActiveClients.StartCleaner(30*time.Second, 20*time.Second)

	go monitor.StartSystemStatsUpdater(10 * time.Second)

	stopCleaner := make(chan struct{})
	go clear.StartRedirectChainCleaner(10*time.Minute, 30*time.Minute, stopCleaner)

	stopAccessCleaner := make(chan struct{})
	go clear.StartAccessCacheCleaner(10*time.Minute, 30*time.Minute, stopAccessCleaner)

	logger.SetupLogger(logger.LogConfig{
		Enabled:    config.Cfg.Log.Enabled,
		File:       config.Cfg.Log.File,
		MaxSizeMB:  config.Cfg.Log.MaxSizeMB,
		MaxBackups: config.Cfg.Log.MaxBackups,
		MaxAgeDays: config.Cfg.Log.MaxAgeDays,
		Compress:   config.Cfg.Log.Compress,
	})

	// 初始化jx处理器
	jxHandler := jx.NewJXHandler(&config.Cfg.JX)
	mux := http.NewServeMux()

	// 启动配置文件自动加载
	go watch.WatchConfigFile(*config.ConfigFilePath)

	// 添加监控路径处理
	monitorPath := config.Cfg.Monitor.Path
	if monitorPath == "" {
		monitorPath = "/status"
	}
	mux.Handle(monitorPath, server.SecurityHeaders(http.HandlerFunc(monitor.Handler)))
	// jx 路径
	jxPath := config.Cfg.JX.Path
	if jxPath == "" {
		jxPath = "/jx"
	}
	mux.Handle(jxPath, server.SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		configHandler.ServeMux(mux)

		webHandler := web.NewWebHandler(configHandler)
		webHandler.ServeMux(mux)
	}

	// 创建默认处理器
	defaultHandler := server.SecurityHeaders(http.HandlerFunc(h.Handler(client)))

	// 检查是否配置了域名映射
	if len(config.Cfg.DomainMap) > 0 {
		// 创建域名映射处理器
		mappings := make(auth.DomainMapList, len(config.Cfg.DomainMap))
		for i, mapping := range config.Cfg.DomainMap {
			mappings[i] = &auth.DomainMapConfig{
				Name:           mapping.Name,
				Source:         mapping.Source,
				Target:         mapping.Target,
				Protocol:       mapping.Protocol,
				Auth:           mapping.Auth,
				ClientHeaders:  mapping.ClientHeaders,
				ServerHeaders:  mapping.ServerHeaders,
			}
		}
		domainMapper := domainmap.NewDomainMapper(mappings, client, defaultHandler)
		// mux.Handle("/", domainMapper)
		mux.Handle("/", server.SecurityHeaders(domainMapper))
	} else {
		// 没有域名映射配置，直接使用默认处理器
		mux.Handle("/", defaultHandler)
	}

	config.ServerCtx, config.Cancel = context.WithCancel(context.Background())

	go func() {
		if err := server.StartHTTPServer(config.ServerCtx, mux); err != nil {
			log.Fatalf("启动HTTP服务器失败: %v", err)
		}
	}()

	<-config.ServerCtx.Done()
	// 收到退出信号，通知清理任务退出
	close(stopCleaner)
	close(stopAccessCleaner)
	config.Cancel()
}
