package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cloudflare/tableflip"
	"github.com/libp2p/go-reuseport"
	"github.com/qist/tvgate/auth"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/domainmap"
	h "github.com/qist/tvgate/handler"
	"github.com/qist/tvgate/jx"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/monitor"
	"github.com/qist/tvgate/publisher"
	httpclient "github.com/qist/tvgate/utils/http"
	tsync "github.com/qist/tvgate/utils/sync"
	"github.com/qist/tvgate/web"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"golang.org/x/net/http2"
)

var (
	serverMu  sync.Mutex
	servers   = make(map[string]*http.Server)
	h3servers = make(map[string]*http3.Server)
	serverWg  tsync.WaitGroup
)

// CloseAllServers 关闭所有正在运行的服务器
func CloseAllServers() {
	serverMu.Lock()

	// 关闭所有HTTP/HTTPS服务器
	for addr, srv := range servers {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := srv.Shutdown(ctx); err != nil {
			logger.LogPrintf("❌ 关闭服务器失败 %s: %v", addr, err)
		} else {
			logger.LogPrintf("✅ 端口 %s 已关闭", addr)
		}
		cancel()
	}

	// 关闭所有HTTP/3服务器
	for addr, srv := range h3servers {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := srv.Shutdown(ctx); err != nil {
			logger.LogPrintf("❌ 关闭HTTP/3服务器失败 %s: %v", addr, err)
		} else {
			logger.LogPrintf("✅ HTTP/3端口 %s 已关闭", addr)
		}
		cancel()
	}

	// 清空maps
	servers = make(map[string]*http.Server)
	h3servers = make(map[string]*http3.Server)
	serverMu.Unlock()

	serverWg.Wait()
}

// ==================== HTTP/TLS 服务器 ====================

func StartHTTPServer(ctx context.Context, addr string, upgrader *tableflip.Upgrader) error {
	return StartHTTPServerWithConfig(ctx, addr, upgrader, &config.Cfg)
}

// StartHTTPServerWithConfig 启动HTTP服务器并使用指定配置
func StartHTTPServerWithConfig(ctx context.Context, addr string, upgrader *tableflip.Upgrader, cfg *config.Config) error {
	mux := RegisterMux(addr, cfg)

	tlsConfig, certFile, keyFile := GetTLSConfig(addr, cfg)
	enableH3 := tlsConfig != nil && addr == fmt.Sprintf(":%d", cfg.Server.TLS.HTTPSPort) && cfg.Server.TLS.EnableH3

	srv := &http.Server{
		Handler:           mux,
		ReadTimeout:       0,
		WriteTimeout:      0,
		IdleTimeout:       60 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		MaxHeaderBytes:    1 << 20,
		TLSConfig:         tlsConfig,
	}

	// ==================== TCP Listener ====================
	var ln net.Listener
	var err error
	if upgrader != nil {
		ln, err = upgrader.Listen("tcp", addr)
		if err != nil {
			// fallback reuseport
			ln, err = reuseport.Listen("tcp", addr)
		}
	} else {
		ln, err = reuseport.Listen("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("❌ 创建 TCP listener 失败: %w", err)
	}

	// ==================== HTTP/3 UDP Listener ====================
	var udpLn net.PacketConn
	var h3srv *http3.Server
	if enableH3 {
		if upgrader != nil {
			udpLn, err = upgrader.ListenPacket("udp", addr)
			if err != nil {
				udpLn, err = net.ListenPacket("udp", addr)
			}
		} else {
			udpLn, err = net.ListenPacket("udp", addr)
		}
		if err != nil {
			return fmt.Errorf("❌ 创建 UDP listener 失败: %w", err)
		}

		h3srv = &http3.Server{
			Addr:        addr,
			Handler:     mux,
			TLSConfig:   tlsConfig,
			IdleTimeout: 120 * time.Second,
			QUICConfig: &quic.Config{
				Allow0RTT:          false, // 禁用 0-RTT 防止重放攻击
				MaxIdleTimeout:     120 * time.Second,
				KeepAlivePeriod:    20 * time.Second,
				MaxIncomingStreams: 65535,  // 最大并发流
				MaxIncomingUniStreams: 65535,
				EnableDatagrams:    true,
				InitialStreamReceiveWindow:     512 * 1024, // 512KB 初始流接收窗口
				InitialConnectionReceiveWindow: 2 * 1024 * 1024, // 2MB 初始连接接收窗口
			},
		}

		serverWg.Go(func() {
			logger.LogPrintf("🚀 启动 HTTP/3 %s", addr)
			if err := h3srv.Serve(udpLn); err != nil && err != http.ErrServerClosed {
				logger.LogPrintf("❌ HTTP/3 错误: %v", err)
			}
		})
	}

	// ==================== 保存到全局 Map ====================
	serverMu.Lock()
	servers[addr] = srv
	if h3srv != nil {
		h3servers[addr] = h3srv
	}
	serverMu.Unlock()

	// ==================== 启动 HTTP/1.x + HTTP/2 ====================
	serverWg.Go(func() {
		if tlsConfig != nil {
			_ = http2.ConfigureServer(srv, &http2.Server{
				MaxConcurrentStreams: 256,
				MaxReadFrameSize:     1 << 14, // 16KB，匹配 TS/FLV 包大小
				IdleTimeout:         60 * time.Second,
			})
			logger.LogPrintf("🚀 启动 HTTPS H1/H2 %s", addr)
			if err := srv.ServeTLS(ln, certFile, keyFile); err != nil && err != http.ErrServerClosed {
				logger.LogPrintf("❌ HTTPS 错误: %v", err)
			}
		} else {
			logger.LogPrintf("🚀 启动 HTTP/1.1 %s", addr)
			if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
				logger.LogPrintf("❌ HTTP 错误: %v", err)
			}
		}
	})

	// ==================== 等待退出 ====================
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.LogPrintf("❌ 关闭 HTTP 服务器失败 %s: %v", addr, err)
		}
		if h3srv != nil {
			if err := h3srv.Shutdown(shutdownCtx); err != nil {
				logger.LogPrintf("❌ 关闭 HTTP/3 服务器失败 %s: %v", addr, err)
			}
		}
		logger.LogPrintf("✅ 端口 %s 已关闭", addr)
	}()

	return nil
}

// 平滑替换所有端口的 Handler
func SetHTTPHandler(addr string, h http.Handler) {
	serverMu.Lock()
	defer serverMu.Unlock()

	if srv, ok := servers[addr]; ok {
		srv.Handler = h
		logger.LogPrintf("🔄 HTTP Handler 已平滑替换 [%s]", addr)
	}
	if h3, ok := h3servers[addr]; ok {
		h3.Handler = h
		logger.LogPrintf("🔄 HTTP/3 Handler 已平滑替换 [%s]", addr)
	}
}

// getTLSConfig 根据端口自动选择对应的 TLS 配置
func GetTLSConfig(addr string, cfg *config.Config) (*tls.Config, string, string) {
	var certFile, keyFile string
	var minVersion, maxVersion uint16
	var cipherSuites []uint16
	var curves []tls.CurveID

	oldAddr := fmt.Sprintf(":%d", cfg.Server.Port)
	newAddr := fmt.Sprintf(":%d", cfg.Server.TLS.HTTPSPort)

	switch addr {
	case oldAddr:
		certFile = cfg.Server.CertFile
		keyFile = cfg.Server.KeyFile
		minVersion, maxVersion = parseProtocols(cfg.Server.SSLProtocols)
		cipherSuites = parseCipherSuites(cfg.Server.SSLCiphers)
		curves = parseCurvePreferences(cfg.Server.SSLECDHCurve)
	case newAddr:
		certFile = cfg.Server.TLS.CertFile
		keyFile = cfg.Server.TLS.KeyFile
		minVersion, maxVersion = parseProtocols(cfg.Server.TLS.Protocols)
		cipherSuites = parseCipherSuites(cfg.Server.TLS.Ciphers)
		curves = parseCurvePreferences(cfg.Server.TLS.ECDHCurve)
	default:
		return nil, "", ""
	}

	if certFile == "" || keyFile == "" {
		return nil, "", ""
	}

	return makeTLSConfig(certFile, keyFile, minVersion, maxVersion, cipherSuites, curves), certFile, keyFile
}

func RegisterMux(addr string, cfg *config.Config) *http.ServeMux {
	mux := http.NewServeMux()

	oldAddr := fmt.Sprintf(":%d", cfg.Server.Port)
	newHTTPAddr := ""
	newHTTPSAddr := ""
	if cfg.Server.HTTPPort > 0 {
		newHTTPAddr = fmt.Sprintf(":%d", cfg.Server.HTTPPort)
	}
	if cfg.Server.TLS.HTTPSPort > 0 {
		newHTTPSAddr = fmt.Sprintf(":%d", cfg.Server.TLS.HTTPSPort)
	}

	// 是否启用了新端口
	hasNewPort := (newHTTPAddr != "" || newHTTPSAddr != "")

	switch {
	case !hasNewPort && addr == oldAddr:
		// 没有新端口 → 旧端口跑全功能
		RegisterFullMux(mux, cfg)

	case hasNewPort && addr == oldAddr:
		// 有新端口 → 旧端口降级成 monitor/web
		RegisterMonitorWebMux(mux, cfg)

	case hasNewPort && addr == newHTTPAddr:
		// 新 HTTP 端口 → jx + 默认代理
		RegisterJXAndProxyMux(mux, cfg)

	case hasNewPort && addr == newHTTPSAddr:
		// 新 HTTPS 端口 → 也只跑 jx + 默认代理
		RegisterJXAndProxyMux(mux, cfg)

	default:
		// 默认兜底 → 只开监控，避免空路由
		RegisterMonitorWebMux(mux, cfg)
	}

	return mux
}

// monitor + web
func RegisterMonitorWebMux(mux *http.ServeMux, cfg *config.Config) {
	monitorPath := cfg.Monitor.Path
	if monitorPath == "" {
		monitorPath = "/status"
	}
	mux.Handle(monitorPath, SecurityHeaders(http.HandlerFunc(monitor.HandleMonitor)))

	if cfg.Web.Enabled {
		webConfig := web.WebConfig{
			Username: cfg.Web.Username,
			Password: cfg.Web.Password,
			Enabled:  cfg.Web.Enabled,
			Path:     cfg.Web.Path,
		}
		configHandler := web.NewConfigHandler(webConfig)
		configHandler.RegisterRoutes(mux)
	}
}

// jx + 默认代理
func RegisterJXAndProxyMux(mux *http.ServeMux, cfg *config.Config) {
	jxHandler := jx.NewJXHandler(&cfg.JX)
	jxPath := cfg.JX.Path
	if jxPath == "" {
		jxPath = "/jx"
	}
	mux.Handle(jxPath, SecurityHeaders(http.HandlerFunc(jxHandler.Handle)))

	// 添加 publisher 路由（如果配置了publisher）
	if cfg.Publisher != nil && cfg.Publisher.Path != "" {
		publisherPath := cfg.Publisher.Path
		if !strings.HasSuffix(publisherPath, "/") {
			publisherPath = publisherPath + "/"
		}
		// 确保不会注册重复的路径
		if publisherPath != "/" {
			mux.Handle(publisherPath, SecurityHeaders(http.StripPrefix(strings.TrimSuffix(publisherPath, "/"), publisher.GetHandler())))
			mux.Handle(strings.TrimSuffix(publisherPath, "/"), SecurityHeaders(http.RedirectHandler(publisherPath, http.StatusMovedPermanently)))
		}
	}

	// 默认启用IPv6以保持向后兼容
	client := httpclient.NewHTTPClient(cfg, nil)
	defaultHandler := SecurityHeaders(http.HandlerFunc(h.Handler(client)))

	if len(cfg.DomainMap) > 0 {
		mappings := make(auth.DomainMapList, len(cfg.DomainMap))
		for i, mapping := range cfg.DomainMap {
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
		localClient := &http.Client{Timeout: cfg.HTTP.Timeout}
		domainMapper := domainmap.NewDomainMapper(mappings, localClient, defaultHandler)
		mux.Handle("/", SecurityHeaders(domainMapper))
	} else {
		mux.Handle("/", defaultHandler)
	}

}

// 全功能 = monitor/web + jx + 默认代理
func RegisterFullMux(mux *http.ServeMux, cfg *config.Config) {
	RegisterMonitorWebMux(mux, cfg)
	RegisterJXAndProxyMux(mux, cfg)
}
