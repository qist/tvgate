package web

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
)
// 二次授权（elevated session）：查看/下载/保存整份配置、备份下载/恢复等
// 敏感操作前需重输登录密码，授权以独立短 TTL Cookie 承载（默认 10 分钟）。

const elevateCookieName = "tvgate_elevate"
const elevateTTLSeconds = 600 // 10 分钟

// isElevated 检查请求是否携带有效的二次授权 Cookie
func (h *ConfigHandler) isElevated(r *http.Request) bool {
	if !h.webConfig.Enabled || h.webConfig.Password == "" {
		// web 未启用或未配置密码时无需二次验证
		return true
	}
	cookie, err := r.Cookie(elevateCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	return h.validateAuthCookie(cookie.Value)
}

// requireElevated 包装敏感 handler：未二次授权时返回 403 JSON（SPA 弹窗引导）
func (h *ConfigHandler) requireElevated(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.isElevated(r) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"code": 403, "msg": "需要二次验证：请输入登录密码"})
			return
		}
		next(w, r)
	}
}

// handleElevate GET 查询授权状态；POST 校验登录密码并颁发短 TTL 授权 Cookie
func (h *ConfigHandler) handleElevate(w http.ResponseWriter, r *http.Request) {
	webPath := h.getWebPath()

	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"elevated": h.isElevated(r),
			"ttl":      elevateTTLSeconds,
		})
		return

	case http.MethodPost:
		var req struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "无效的请求数据", http.StatusBadRequest)
			return
		}
		// 与登录一致的空口令加固 + 常量时间比较
		if h.webConfig.Password == "" ||
			req.Password == "" ||
			subtle.ConstantTimeCompare([]byte(req.Password), []byte(h.webConfig.Password)) != 1 {
			http.Error(w, "密码错误", http.StatusUnauthorized)
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     elevateCookieName,
			Value:    h.generateAuthCookieValue(h.webConfig.Username),
			Path:     webPath,
			HttpOnly: true,
			Secure:   r.TLS != nil,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   elevateTTLSeconds,
		})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "success", "ttl": elevateTTLSeconds})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}
