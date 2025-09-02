package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

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

func StartHTTPServer(ctx context.Context, handler http.Handler) error {
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

	// HTTP/1.x + HTTP/2 server
	srv := &http.Server{
		Handler:           handler,
		ReadTimeout:       0,
		WriteTimeout:      0,
		IdleTimeout:       60 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		MaxHeaderBytes:    1 << 20,
		TLSConfig:         tlsConfig,
	}

	// HTTP/3 server
	var h3srv *http3.Server
	if tlsConfig != nil {
		h3srv = &http3.Server{
			Addr:        addr,
			Handler:     handler,
			TLSConfig:   tlsConfig,
			IdleTimeout: 60 * time.Second,
			QUICConfig: &quic.Config{
				Allow0RTT:          true,
				MaxIdleTimeout:     time.Second * 60,
				KeepAlivePeriod:    time.Second * 20,
				MaxIncomingStreams: 10000,
				EnableDatagrams:    true,
			},
		}
	}

	// 锁定旧 server 并替换为新 server
	currentMu.Lock()
	oldSrv := currentSrv
	oldH3 := currentH3
	currentSrv = srv
	currentH3 = h3srv
	currentMu.Unlock()

	// 关闭旧 HTTP/1.x/2 顺序化
	if oldSrv != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := oldSrv.Shutdown(shutdownCtx); err != nil {
			// logger.LogPrintf("❌ 关闭旧 HTTP/1.x/2 失败: %v", err)
		} else {
			logger.LogPrintf("✅ 旧 HTTP/1.x/2 已关闭")
		}
	}

	// 关闭旧 HTTP/3，顺序化
	if oldH3 != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := oldH3.Shutdown(shutdownCtx); err != nil {
			logger.LogPrintf("⚠️ 关闭旧 HTTP/3 出现问题: %v", err)
			// 强制等待一段时间，确保端口释放
			time.Sleep(time.Second * 2)
		} else {
			logger.LogPrintf("✅ 旧 HTTP/3 已关闭")
		}
		// 额外等待时间，确保 QUIC 连接完全清理
		time.Sleep(time.Second)
	}

	// 启动 HTTP/1.x (SO_REUSEPORT)
	go func() {
		var ln net.Listener
		var err error
		if tlsConfig != nil {
			ln, err = reuseport.Listen("tcp", addr)
			if err != nil {
				logger.LogPrintf("❌ 创建 H1 Listener 失败: %v", err)
				return
			}
			_ = http2.ConfigureServer(srv, &http2.Server{})
			logger.LogPrintf("🚀 启动 HTTPS H1/H2 %s", addr)
			if err := srv.ServeTLS(ln, certFile, keyFile); err != nil && err != http.ErrServerClosed {
				logger.LogPrintf("❌ HTTP/1.x/2 错误: %v", err)
			}
		} else {
			ln, err = reuseport.Listen("tcp", addr)
			if err != nil {
				logger.LogPrintf("❌ 创建 H1 Listener 失败: %v", err)
				return
			}
			logger.LogPrintf("🚀 启动 HTTP/1.1 %s", addr)
			if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
				logger.LogPrintf("❌ HTTP/1.x 错误: %v", err)
			}
		}
	}()

	// 启动 HTTP/3 并行
	if h3srv != nil {
		go func(h3 *http3.Server) {
			maxRetries := 5
			retryDelay := time.Second * 3

			for retry := 0; retry < maxRetries; retry++ {
				if retry > 0 {
					logger.LogPrintf("⚠️ 正在重试启动 HTTP/3 (第 %d 次)", retry)
					time.Sleep(retryDelay)
				}

				// 尝试启动前先检查端口是否可用
				conn, err := net.ListenPacket("udp", addr)
				if err != nil {
					logger.LogPrintf("⚠️ HTTP/3 端口检查失败: %v, 等待重试...", err)
					continue
				}
				conn.Close()

				// 清理旧连接
				if retry > 0 {
					logger.LogPrintf("🧹 清理 QUIC 旧连接...")
					time.Sleep(time.Second)
				}

				logger.LogPrintf("🚀 启动 HTTP/3 %s", addr)
				err = h3.ListenAndServe()
				if err == nil || err == http.ErrServerClosed {
					return
				}

				logger.LogPrintf("❌ HTTP/3 启动失败: %v", err)
				if retry == maxRetries-1 {
					logger.LogPrintf("❌ HTTP/3 重试次数已达上限，放弃启动")
				}
			}
		}(h3srv)
	}

	// 等待退出
	<-ctx.Done()

	// 优雅关闭
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
