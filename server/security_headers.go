package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/qist/tvgate/config"
)

// isStreamingPath 判断是否为流媒体路径
// 流媒体路径不设置 Alt-Svc / HSTS / Connection: close
// 避免播放器尝试协议切换导致重连
func isStreamingPath(path string) bool {
	return strings.HasPrefix(path, "/udp/") ||
		strings.HasPrefix(path, "/rtp/") ||
		strings.HasPrefix(path, "/rtsp/")
}

// isLiveStreamPath 判断是否为长连接直播流路径（FLV）
// TS 是短连接下载（走 CopyTSWithCache），不是持续推流
func isLiveStreamPath(path string) bool {
	p := strings.ToLower(path)
	return strings.HasSuffix(p, ".flv")
}

func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		streaming := isStreamingPath(r.URL.Path) || isLiveStreamPath(r.URL.Path)

		// 1️⃣ 强制 HTTPS (HSTS) — 仅对非流媒体请求设置
		// 流媒体请求设置 HSTS/Alt-Svc 会导致播放器尝试协议切换，引发重连
		if !streaming && config.Cfg.Server.CertFile != "" && config.Cfg.Server.KeyFile != "" {
			w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")

			// QUIC / HTTP3 提示 — 使用正确的端口
			// 优先使用 TLS.HTTPSPort，其次用 Server.Port
			h3Port := config.Cfg.Server.TLS.HTTPSPort
			if h3Port == 0 {
				h3Port = config.Cfg.Server.Port
			}
			if config.Cfg.Server.TLS.EnableH3 {
				altSvc := fmt.Sprintf(`h3=":%d"; ma=86400`, h3Port)
				w.Header().Set("Alt-Svc", altSvc)
				w.Header().Set("X-QUIC", "h3")
			}
		}

		// 2️⃣ 统一启用 HTTP/1.1 keep-alive
		// 流媒体路径与普通路径都复用连接，配合 Content-Length 透传降低建连开销；
		// 不再对非流媒体路径强制 Connection: close。

		// 3️⃣ 调用下一个 handler
		next.ServeHTTP(w, r)
	})
}
