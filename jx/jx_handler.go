package jx

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/qist/tvgate/config"
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
	jxParam := r.URL.Query().Get("jx")
	idParam := r.URL.Query().Get("id")
	if idParam == "" {
		idParam = h.Config.DefaultID
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Server", "TVGate")

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
