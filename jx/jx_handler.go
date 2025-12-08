package jx

import (
	"crypto/md5"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"

	"fmt"
	"github.com/qist/tvgate/auth"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/monitor"
	"time"
)

type VideoSourceHandler func(w http.ResponseWriter, r *http.Request, link, id string)

var sourceMap = map[string]VideoSourceHandler{}

// RegisterVideoSource 注册视频源处理函数
func RegisterVideoSource(domain string, handler VideoSourceHandler) {
	sourceMap[strings.ToLower(domain)] = handler
}

type JXHandler struct {
	Config *config.JXConfig
}

func NewJXHandler(cfg *config.JXConfig) *JXHandler {
	return &JXHandler{Config: cfg}
}

// Handle JX 请求入口
func (h *JXHandler) Handle(w http.ResponseWriter, r *http.Request) {
	// 注册活跃客户端
	clientIP := monitor.GetClientIP(r)
	raw := fmt.Sprintf("%s/%s", r.URL.Path, r.URL.RawQuery)
	hi := md5.Sum([]byte(raw))
	hashStr := hex.EncodeToString(hi[:])
	connID := clientIP + "_" + hashStr
	// 全局token验证
	if auth.GetGlobalTokenManager() != nil {
		tokenParamName := "my_token" // 默认参数名
		token := r.URL.Query().Get(tokenParamName)

		// // 获取客户端真实IP
		// clientIP := monitor.GetClientIP(r)

		// // 构造连接ID（IP+端口）
		// connID := clientIP + "_" + r.RemoteAddr

		// 验证全局token
		if !auth.GetGlobalTokenManager().ValidateToken(token, r.URL.Path, connID) {
			logger.LogPrintf("全局token验证失败: token=%s, path=%s, ip=%s", token, r.URL.Path, clientIP)
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		// 更新全局token活跃状态
		auth.GetGlobalTokenManager().KeepAlive(token, connID, clientIP, r.URL.Path)
		logger.LogPrintf("全局token验证成功: token=%s, path=%s, ip=%s", token, r.URL.Path, clientIP)
	}
	// 如果启用了全局认证，在向后端发送请求前删除 token 参数（保持原始 URL）
	if auth.GetGlobalTokenManager() != nil {
		tokenParamName := "my_token"
		if auth.GetGlobalTokenManager().TokenParamName != "" {
			tokenParamName = auth.GetGlobalTokenManager().TokenParamName
		}

		cleanURL := removeQueryParamRaw(r.URL.String(), tokenParamName)
		logger.LogPrintf("清理后的URL: %s", cleanURL)
	}
	monitor.ActiveClients.Register(connID, &monitor.ClientConnection{
		IP:             clientIP,
		URL:            r.URL.Path,
		UserAgent:      r.UserAgent(),
		ConnectionType: strings.ToUpper(r.URL.Scheme),
		ConnectedAt:    time.Now(),
		LastActive:     time.Now(),
	})
	defer monitor.ActiveClients.Unregister(connID, strings.ToUpper(r.URL.Scheme))

	jxParam := r.URL.Query().Get("jx")
	idParam := r.URL.Query().Get("id")
	if idParam == "" {
		idParam = h.Config.DefaultID
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// w.Header().Set("Server", "TVGate")

	if jxParam == "" {
		JSONResponse(w, map[string]interface{}{"error": "jx 参数不能为空"})
		return
	}

	// 如果是完整 URL
	if strings.HasPrefix(jxParam, "http://") || strings.HasPrefix(jxParam, "https://") {
		if handler := matchSourceHandler(jxParam); handler != nil {
			handler(w, r, jxParam, idParam)
			return
		}
	}

	// 默认走普通 API 查询
	h.HandleRequest(w, r, jxParam, idParam)
}

// matchSourceHandler 根据 URL host 返回对应处理函数
func matchSourceHandler(rawURL string) VideoSourceHandler {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	host := strings.ToLower(u.Host)
	for key, handler := range sourceMap {
		if strings.Contains(host, key) {
			return handler
		}
	}
	return nil
}

// removeQueryParamRaw 保持原始 query 格式，只删除指定参数
func removeQueryParamRaw(rawURL, key string) string {
	parts := strings.SplitN(rawURL, "?", 2)
	if len(parts) != 2 {
		// 没有 query，直接返回原始 URL
		return rawURL
	}
	base := parts[0]
	query := parts[1]

	newParts := []string{}
	for _, kv := range strings.Split(query, "&") {
		if kv == "" || strings.HasPrefix(kv, key+"=") {
			continue
		}
		newParts = append(newParts, kv)
	}

	if len(newParts) > 0 {
		return base + "?" + strings.Join(newParts, "&")
	}
	return base
}
