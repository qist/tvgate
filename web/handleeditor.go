package web

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/qist/tvgate/config"
)
// handleEditor 处理配置编辑器页面请求
func (h *ConfigHandler) handleEditor(w http.ResponseWriter, r *http.Request) {
	// 获取配置的Web路径，默认为/web/
	webPath := h.webConfig.Path
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
		// 只允许GET方法
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// 检查是否有domainmap配置
		config.CfgMu.RLock()
		hasDomainMap := len(config.Cfg.DomainMap) > 0

		// 检查proxygroups是否配置了有效内容
		hasProxyGroups := len(config.Cfg.ProxyGroups) > 0
		config.CfgMu.RUnlock()

		// 渲染编辑器模板
		data := map[string]interface{}{
			"title":          "TVGate 配置编辑器",
			"webPath":        webPath,
			"hasDomainMap":   hasDomainMap,
			"hasProxyGroups": hasProxyGroups,
			"configPath":     *config.ConfigFilePath, // 添加配置文件路径到模板数据
		}

		// 从嵌入的文件系统读取模板
		content, err := templatesFS.ReadFile("templates/editor.html")
		if err != nil {
			http.Error(w, "Failed to read template file: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 解析模板
		tmpl, err := template.New("editor").Parse(string(content))
		if err != nil {
			http.Error(w, "Failed to parse template: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 读取侧边栏模板
		sidebarContent, err := templatesFS.ReadFile("templates/sidebar.html")
		if err != nil {
			http.Error(w, "Failed to read sidebar template file: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 解析侧边栏模板
		_, err = tmpl.New("sidebar").Parse(string(sidebarContent))
		if err != nil {
			http.Error(w, "Failed to parse sidebar template: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 设置响应头
		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		// 执行模板
		if err := tmpl.Execute(w, data); err != nil {
			http.Error(w, "Failed to execute template: "+err.Error(), http.StatusInternalServerError)
			return
		}

		return
	}

	http.NotFound(w, r)
}

// handleNodeEditor 处理节点编辑器页面请求
func (h *ConfigHandler) handleNodeEditor(w http.ResponseWriter, r *http.Request) {
	// 获取配置的Web路径，默认为/web/
	webPath := h.webConfig.Path
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

	// 如果请求的是节点编辑器路径
	if r.URL.Path == webPath+"node-editor" {
		// 只允许GET方法
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// 获取参数
		nodeName := r.URL.Query().Get("node")
		groupName := r.URL.Query().Get("group")

		// 渲染节点编辑器模板
		data := map[string]interface{}{
			"webPath": webPath,
			"node":    nodeName,
			"group":   groupName,
		}

		// 设置标题
		if nodeName != "" {
			data["title"] = fmt.Sprintf("节点配置编辑器 - %s", nodeName)
		} else {
			data["title"] = "节点配置编辑器"
		}

		// 从嵌入的文件系统读取模板
		content, err := templatesFS.ReadFile("templates/node_editor.html")
		if err != nil {
			http.Error(w, "Failed to read template file: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 解析模板
		tmpl, err := template.New("node_editor").Parse(string(content))
		if err != nil {
			http.Error(w, "Failed to parse template: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 设置响应头
		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		// 执行模板
		if err := tmpl.Execute(w, data); err != nil {
			http.Error(w, "Failed to execute template: "+err.Error(), http.StatusInternalServerError)
			return
		}

		return
	}

	http.NotFound(w, r)
}