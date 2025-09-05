package web

import (
	"embed"
	"fmt"
	"net/http"
	"os"
	"strings"
	"text/template"

	"github.com/qist/tvgate/config"
)

//go:embed templates/*.html
var templatesFS embed.FS

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

// renderTemplate 渲染指定的模板
func (h *WebHandler) renderTemplate(w http.ResponseWriter, r *http.Request, tmplName, filePath string, data map[string]interface{}) error {
	// 从嵌入的文件系统读取模板
	content, err := templatesFS.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read template file: %v", err)
	}

	// 解析模板
	tmpl, err := template.New(tmplName).Parse(string(content))
	if err != nil {
		return fmt.Errorf("failed to parse template: %v", err)
	}

	// 设置响应头
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	
	// 执行模板
	return tmpl.Execute(w, data)
}

// handleWeb 处理web管理界面首页
func (h *WebHandler) handleWeb(w http.ResponseWriter, r *http.Request) {
	// 获取配置的Web路径，默认为/web/
	webPath := config.Cfg.Web.Path
	if webPath == "" {
		webPath = "/web/"
	}
	
	// 确保路径以/开头和结尾
	if !strings.HasPrefix(webPath, "/") {
		webPath = "/" + webPath
	}
	if !strings.HasSuffix(webPath, "/") {
		webPath = webPath + "/"
	}
	
	// 如果请求的是根路径，则返回首页
	if r.URL.Path == webPath {
		data := map[string]interface{}{
			"title": "TVGate Web Management",
		}

		if err := h.renderTemplate(w, r, "index", "templates/index.html", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	// 如果请求的路径不是web管理路径，则重定向到web管理路径
	if !strings.HasPrefix(r.URL.Path, webPath) {
		http.Redirect(w, r, webPath, http.StatusMovedPermanently)
		return
	}

	http.NotFound(w, r)
}

// handleEditor 处理配置编辑器页面
func (h *WebHandler) handleEditor(w http.ResponseWriter, r *http.Request) {
	// 获取配置的Web路径，默认为/web/
	webPath := config.Cfg.Web.Path
	if webPath == "" {
		webPath = "/web/"
	}
	
	// 确保路径以/开头和结尾
	if !strings.HasPrefix(webPath, "/") {
		webPath = "/" + webPath
	}
	if !strings.HasSuffix(webPath, "/") {
		webPath = webPath + "/"
	}
	
	// 如果请求的是编辑器路径
	if r.URL.Path == webPath+"editor" {
		// 读取配置文件内容
		configPath := *config.ConfigFilePath
		contentBytes, err := os.ReadFile(configPath)
		if err != nil {
			http.Error(w, "Failed to read config file: "+err.Error(), http.StatusInternalServerError)
			return
		}

		data := map[string]interface{}{
			"title":        "Configuration Editor",
			"configPath":   configPath,
			"configString": string(contentBytes),
		}

		if err := h.renderTemplate(w, r, "editor", "templates/editor.html", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	http.NotFound(w, r)
}
