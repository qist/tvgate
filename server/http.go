package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/cloudflare/tableflip"
	"github.com/libp2p/go-reuseport"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"golang.org/x/net/http2"
)

var (
	currentSrv *http.Server
	currentH3  *http3.Server
	currentMu  sync.Mutex
)

// StartHTTPServer 启动 HTTP/1.x、HTTP/2 和 HTTP/3，支持 tableflip 热更
func StartHTTPServer(ctx context.Context, handler http.Handler, upgrader *tableflip.Upgrader) error {
	addr := fmt.Sprintf(":%d", config.Cfg.Server.Port)
	certFile := config.Cfg.Server.CertFile
	keyFile := config.Cfg.Server.KeyFile

	minVersion, maxVersion := parseProtocols(config.Cfg.Server.SSLProtocols)
	cipherSuites := parseCipherSuites(config.Cfg.Server.SSLCiphers)
	curves := parseCurvePreferences(config.Cfg.Server.SSLECDHCurve)

	var tlsConfig *tls.Config
	if certFile != "" && keyFile != "" {
		tlsConfig = makeTLSConfig(certFile, keyFile, minVersion, maxVersion, cipherSuites, curves)
	}

	// 创建 HTTP server
	srv := &http.Server{
		Handler:           handler,
		ReadTimeout:       0,
		WriteTimeout:      0,
		IdleTimeout:       60 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		MaxHeaderBytes:    1 << 20,
		TLSConfig:         tlsConfig,
	}

	// TCP listener
	var ln net.Listener
	var err error
	if upgrader != nil {
		ln, err = upgrader.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("❌ upgrader 创建 TCP listener 失败: %w", err)
		}
	} else {
		ln, err = reuseport.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("❌ 创建 TCP listener 失败: %w", err)
		}
	}

	// UDP listener（HTTP/3）
	var udpLn net.PacketConn
	var h3srv *http3.Server
	if tlsConfig != nil && upgrader != nil {
		udpLn, err = upgrader.ListenPacket("udp", addr)
		if err != nil {
			return fmt.Errorf("❌ upgrader 创建 UDP listener 失败: %w", err)
		}

		h3srv = &http3.Server{
			Addr:      addr,
			Handler:   handler,
			TLSConfig: tlsConfig,
			IdleTimeout: 60 * time.Second,
			QUICConfig: &quic.Config{
				Allow0RTT:          true,
				MaxIdleTimeout:     time.Second * 60,
				KeepAlivePeriod:    time.Second * 20,
				MaxIncomingStreams: 10000,
				EnableDatagrams:    true,
			},
		}

		go func() {
			logger.LogPrintf("🚀 启动 HTTP/3 %s", addr)
			if err := h3srv.Serve(udpLn); err != nil && err != http.ErrServerClosed {
				logger.LogPrintf("❌ HTTP/3 错误: %v", err)
			}
		}()
	}

	// 替换全局 server
	currentMu.Lock()
	oldSrv := currentSrv
	oldH3 := currentH3
	currentSrv = srv
	currentH3 = h3srv
	currentMu.Unlock()

	// 优雅关闭旧 HTTP/1.x/2
	if oldSrv != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := oldSrv.Shutdown(shutdownCtx); err == nil {
			logger.LogPrintf("✅ 旧 HTTP/1.x/2 已关闭")
		}
	}

	// 优雅关闭旧 HTTP/3
	if oldH3 != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := oldH3.Shutdown(shutdownCtx); err == nil {
			logger.LogPrintf("✅ 旧 HTTP/3 已关闭")
		}
		time.Sleep(time.Second)
	}

	// 启动 HTTP/1.x + HTTP/2
	go func() {
		if tlsConfig != nil {
			_ = http2.ConfigureServer(srv, &http2.Server{})
			logger.LogPrintf("🚀 启动 HTTPS H1/H2 %s", addr)
			if err := srv.ServeTLS(ln, certFile, keyFile); err != nil && err != http.ErrServerClosed {
				logger.LogPrintf("❌ HTTP/1.x/2 错误: %v", err)
			}
		} else {
			logger.LogPrintf("🚀 启动 HTTP/1.1 %s", addr)
			if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
				logger.LogPrintf("❌ HTTP/1.x 错误: %v", err)
			}
		}
	}()

	// 等待退出
	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.LogPrintf("❌ HTTP/1.x/2 关闭失败: %v", err)
	}
	if h3srv != nil {
		if err := h3srv.Shutdown(shutdownCtx); err != nil {
			logger.LogPrintf("❌ HTTP/3 关闭失败: %v", err)
		}
	}

	logger.LogPrintf("✅ 所有服务器已关闭")
	return nil
}

// SetHTTPHandler 平滑替换当前 HTTP Handler
func SetHTTPHandler(h http.Handler) {
	currentMu.Lock()
	defer currentMu.Unlock()

	if currentSrv != nil {
		currentSrv.Handler = h
	}
	if currentH3 != nil {
		currentH3.Handler = h
	}
	logger.LogPrintf("🔄 HTTP Handler 已平滑替换")
}
