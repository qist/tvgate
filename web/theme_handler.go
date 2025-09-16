package web

import (
	"encoding/json"
	"net/http"
	"sync"
)

var (
	currentTheme = "dark"
	themeMutex   sync.RWMutex
)

// ThemeHandler 处理主题同步
type ThemeHandler struct{}

// NewThemeHandler 创建主题处理器
func NewThemeHandler() *ThemeHandler {
	return &ThemeHandler{}
}

// ServeHTTP 处理主题同步请求
func (h *ThemeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.getTheme(w, r)
	case http.MethodPost:
		h.setTheme(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// getTheme 获取当前主题
func (h *ThemeHandler) getTheme(w http.ResponseWriter, r *http.Request) {
	themeMutex.RLock()
	defer themeMutex.RUnlock()

	json.NewEncoder(w).Encode(map[string]string{
		"theme": currentTheme,
	})
}

// setTheme 设置并同步主题
func (h *ThemeHandler) setTheme(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Theme string `json:"theme"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.Theme != "dark" && req.Theme != "light" {
		http.Error(w, "Invalid theme value", http.StatusBadRequest)
		return
	}

	themeMutex.Lock()
	currentTheme = req.Theme
	themeMutex.Unlock()

	w.WriteHeader(http.StatusOK)
}

// GetCurrentTheme 获取当前主题
func GetCurrentTheme() string {
	themeMutex.RLock()
	defer themeMutex.RUnlock()
	return currentTheme
}