package server

import (
	"net/http"

	"github.com/qist/tvgate/auth"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/monitor"
)

// globalAuth 包装 /pp 播放页 handler：config 的 global_auth 启用时必须携带有效 token。
//
// 为什么页面也要挡：/api/player/* 与 /player/ 早就由 player 包的 requireToken 挡住了，
// 但页面本体（player.html）一直是裸放行——没开授权时谁都能打开播放页；
// 开了授权后页面照样 200，只是接口 403（表现成"能打开但一片空白"）。
//
// global_auth 未启用时 GetGlobalTokenManager() 返回 nil，直接放行，行为与从前一致。
func globalAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gt := auth.GetGlobalTokenManager()
		if gt == nil {
			next(w, r)
			return
		}
		clientIP := monitor.GetClientIP(r)
		connID := clientIP + "_" + r.URL.Path
		token := auth.ExtractToken(r, gt)
		if !gt.ValidateToken(token, r.URL.Path, connID) {
			logger.LogPrintf("[auth] 拒绝未授权访问 %s %s ip=%s", r.Method, r.URL.Path, clientIP)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("Forbidden"))
			return
		}
		gt.KeepAlive(token, connID, clientIP, r.URL.Path)
		next(w, r)
	}
}
