package server

import (
	"fmt"
	"github.com/qist/tvgate/config"
	"net/http"
	"strings"
)

func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1️⃣ 强制 HTTPS (HSTS)
		if config.Cfg.Server.CertFile != "" && config.Cfg.Server.KeyFile != "" {
			w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")

			// QUIC / HTTP3 提示
			altSvc := fmt.Sprintf(`h3=":%d"; ma=86400, h3-29=":%d"; ma=86400`, config.Cfg.Server.Port, config.Cfg.Server.Port)
			w.Header().Set("Alt-Svc", altSvc)
			w.Header().Set("X-QUIC", "h3")
		}

		// 2️⃣ 禁止缓存
		// w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		// w.Header().Set("Pragma", "no-cache")
		// w.Header().Set("Expires", "0")
		// r.Header.Set("Connection", "close")
		// 3️⃣ 智能关闭 HTTP/1.1 keep-alive

		switch {
		case strings.HasPrefix(r.URL.Path, "/udp/"):

		case strings.HasPrefix(r.URL.Path, "/rtp/"):

		default:
			if r.ProtoMajor == 1 {
				w.Header().Set("Connection", "close")
			}
		}

		// 4️⃣ 调用下一个 handler
		next.ServeHTTP(w, r)
	})
}
