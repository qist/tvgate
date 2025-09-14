package web

import (
	"net/http"
)

// WebHandler web管理界面处理器
type WebHandler struct {
	configHandler *ConfigHandler
}

// NewWebHandler 创建web管理界面处理器
func NewWebHandler(configHandler *ConfigHandler) *WebHandler {
	return &WebHandler{
		configHandler: configHandler,
	}
}

// ServeMux 注册web管理界面路由
func (h *WebHandler) ServeMux(mux *http.ServeMux) {
	// 路由已在config_handler.go中注册，此处无需重复注册
}

// handleWeb 处理web管理界面首页
// 注意：这个函数的正确实现在config_handler.go中

// handleEditor 处理配置编辑器页面
// 注意：这个函数的正确实现在config_handler.go中

