package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	// "io/fs"
	// "log"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/qist/tvgate/config"
	"gopkg.in/yaml.v3"
)

//go:embed templates/*
var templatesFS embed.FS

// WebConfig web管理界面配置
type WebConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	Enabled  bool   `yaml:"enabled"`
	Path     string `yaml:"path"` // Web管理界面的访问路径
}

// ConfigHandler 配置文件管理处理器
type ConfigHandler struct {
	webConfig WebConfig
}

// NewConfigHandler 创建配置管理处理器
func NewConfigHandler(webConfig WebConfig) *ConfigHandler {
	return &ConfigHandler{
		webConfig: webConfig,
	}
}

// basicAuth 中间件，用于基本认证
func (h *ConfigHandler) basicAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 如果未启用web管理，则返回404
		if !h.webConfig.Enabled {
			http.NotFound(w, r)
			return
		}

		username, password, ok := r.BasicAuth()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// 比较用户名和密码（使用常量时间比较防止时序攻击）
		if subtle.ConstantTimeCompare([]byte(username), []byte(h.webConfig.Username)) != 1 ||
			subtle.ConstantTimeCompare([]byte(password), []byte(h.webConfig.Password)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		handler(w, r)
	}
}

// cookieAuth 中间件，用于基于Cookie的认证
func (h *ConfigHandler) cookieAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 如果未启用web管理，则返回404
		if !h.webConfig.Enabled {
			http.NotFound(w, r)
			return
		}

		// 检查用户是否已认证
		if !h.isAuthenticated(r) {
			// 未认证用户重定向到登录页面
			webPath := h.getWebPath()
			http.Redirect(w, r, webPath+"login", http.StatusFound)
			return
		}

		handler(w, r)
	}
}

// renderTemplate 渲染指定的模板
func (h *ConfigHandler) renderTemplate(w http.ResponseWriter, r *http.Request, tmplName, filePath string, data map[string]interface{}) error {
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

// ServeMux 注册配置管理路由
func (h *ConfigHandler) ServeMux(mux *http.ServeMux) {
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

	// 注册不需要认证的路由
	mux.HandleFunc(webPath, h.handleWeb)
	mux.HandleFunc(webPath+"login", h.handleLogin)
	mux.HandleFunc(webPath+"logout", h.handleLogout)
	mux.HandleFunc(webPath+"auth-status", h.handleAuthStatus)

	// 注册需要认证的路由，使用基于Cookie的认证中间件
	mux.HandleFunc(webPath+"editor", h.cookieAuth(h.handleEditor))
	mux.HandleFunc(webPath+"node-editor", h.cookieAuth(h.handleNodeEditor))
	mux.HandleFunc(webPath+"group-editor", h.cookieAuth(h.handleGroupEditor))
	mux.HandleFunc(webPath+"domainmap-editor", h.cookieAuth(h.handleDomainMapEditor))
	mux.HandleFunc(webPath+"global-auth-editor", h.cookieAuth(h.handleGlobalAuthEditor))
	mux.HandleFunc(webPath+"config", h.cookieAuth(h.handleConfig))
	mux.HandleFunc(webPath+"config/save", h.cookieAuth(h.handleConfigSave))
	mux.HandleFunc(webPath+"config/save-node", h.cookieAuth(h.handleConfigSaveNode))
	mux.HandleFunc(webPath+"config/save-group", h.cookieAuth(h.handleConfigSaveGroup))
	mux.HandleFunc(webPath+"config/save-domainmap", h.cookieAuth(h.handleDomainMapConfigSave))
	mux.HandleFunc(webPath+"config/save-global-auth", h.cookieAuth(h.handleGlobalAuthConfigSave))
	mux.HandleFunc(webPath+"config/validate", h.cookieAuth(h.handleConfigValidate))
	mux.HandleFunc(webPath+"config/node", h.cookieAuth(h.handleNodeConfig))
	mux.HandleFunc(webPath+"config/group", h.cookieAuth(h.handleGroupConfig))
	mux.HandleFunc(webPath+"config/domainmap", h.cookieAuth(h.handleDomainMapConfig))
	mux.HandleFunc(webPath+"config/global-auth", h.cookieAuth(h.handleGlobalAuthConfig))
}

// handleWeb 处理web管理界面首页
func (h *ConfigHandler) handleWeb(w http.ResponseWriter, r *http.Request) {
	// 记录请求信息用于调试
	// log.Printf("处理Web请求: Path=%s, Method=%s", r.URL.Path, r.Method)

	// 检查用户是否已认证
	isAuthenticated := h.isAuthenticated(r)
	// log.Printf("认证状态: %t", isAuthenticated)

	if !isAuthenticated {
		// 未认证用户重定向到登录页面
		webPath := h.getWebPath()
		redirectURL := webPath + "login"
		// log.Printf("用户未认证，重定向到: %s", redirectURL)
		http.Redirect(w, r, redirectURL, http.StatusFound)
		return
	}

	// 获取配置的Web路径，默认为/web/
	webPath := h.getWebPath()
	// log.Printf("Web路径配置: %s", webPath)

	// 如果请求的是根路径，则返回首页
	// 处理多种情况：精确匹配、带斜杠和不带斜杠的路径
	pathMatch := r.URL.Path == webPath ||
		r.URL.Path == strings.TrimSuffix(webPath, "/") ||
		r.URL.Path == "/"+strings.TrimPrefix(webPath, "/") ||
		r.URL.Path == "/"+strings.TrimSuffix(strings.TrimPrefix(webPath, "/"), "/")

	// log.Printf("路径匹配结果: %t (请求路径: %s, 配置路径: %s)", pathMatch, r.URL.Path, webPath)

	if pathMatch {
		// 获取监控路径，默认为/status
		monitorPath := config.Cfg.Monitor.Path
		if monitorPath == "" {
			monitorPath = "/status"
		}

		// 检查是否有domainmap和global_auth配置
		hasDomainMap := len(config.Cfg.DomainMap) > 0

		// 检查global_auth是否配置了有效内容
		globalAuth := config.Cfg.GlobalAuth
		hasGlobalAuth := globalAuth.TokensEnabled ||
			globalAuth.TokenParamName != "" ||
			globalAuth.DynamicTokens.EnableDynamic ||
			globalAuth.DynamicTokens.Secret != "" ||
			globalAuth.DynamicTokens.Salt != "" ||
			globalAuth.StaticTokens.EnableStatic ||
			globalAuth.StaticTokens.Token != ""

		data := map[string]interface{}{
			"title":         "TVGate Web管理",
			"webPath":       webPath,
			"monitorPath":   monitorPath,
			"hasDomainMap":  hasDomainMap,
			"hasGlobalAuth": hasGlobalAuth,
		}

		// 从嵌入的文件系统读取模板
		content, err := templatesFS.ReadFile("templates/index.html")
		if err != nil {
			// log.Printf("读取模板文件失败: %v", err)
			http.Error(w, "Failed to read template file", http.StatusInternalServerError)
			return
		}

		tmpl, err := template.New("index").Parse(string(content))
		if err != nil {
			// log.Printf("解析模板失败: %v", err)
			http.Error(w, "Failed to parse template", http.StatusInternalServerError)
			return
		}

		// 设置响应头
		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		// 执行模板
		if err := tmpl.Execute(w, data); err != nil {
			// log.Printf("执行模板失败: %v", err)
			http.Error(w, "Failed to execute template", http.StatusInternalServerError)
			return
		}

		// log.Printf("成功渲染首页")
		return
	}

	// log.Printf("路径不匹配，返回404")
	http.NotFound(w, r)
}

// handleLogin 处理登录页面和登录请求
func (h *ConfigHandler) handleLogin(w http.ResponseWriter, r *http.Request) {
	// log.Printf("处理登录请求: Path=%s, Method=%s", r.URL.Path, r.Method)

	webPath := h.getWebPath()
	// log.Printf("Web路径配置: %s", webPath)

	// GET请求显示登录页面
	if r.Method == http.MethodGet {
		// 如果用户已经登录，重定向到首页
		if h.isAuthenticated(r) {
			redirectURL := strings.TrimSuffix(webPath, "/")
			// log.Printf("用户已认证，重定向到首页: %s", redirectURL)
			http.Redirect(w, r, redirectURL, http.StatusFound)
			return
		}

		// log.Printf("显示登录页面")

		// 显示登录页面
		content, err := templatesFS.ReadFile("templates/login.html")
		if err != nil {
			// log.Printf("读取登录模板文件失败: %v", err)
			http.Error(w, "Failed to read template file", http.StatusInternalServerError)
			return
		}

		tmpl, err := template.New("login").Parse(string(content))
		if err != nil {
			// log.Printf("解析登录模板失败: %v", err)
			http.Error(w, "Failed to parse template", http.StatusInternalServerError)
			return
		}

		data := map[string]interface{}{
			"title":   "TVGate Web管理 - 登录",
			"webPath": webPath,
		}

		// 设置响应头
		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		// 执行模板
		if err := tmpl.Execute(w, data); err != nil {
			// log.Printf("执行登录模板失败: %v", err)
			http.Error(w, "Failed to execute template", http.StatusInternalServerError)
			return
		}

		// log.Printf("成功渲染登录页面")
		return
	}

	// POST请求处理登录
	if r.Method == http.MethodPost {
		// log.Printf("处理登录表单提交")

		// 解析请求体
		var credentials struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}

		if err := json.NewDecoder(r.Body).Decode(&credentials); err != nil {
			// log.Printf("解析登录请求体失败: %v", err)
			http.Error(w, "无效的请求数据", http.StatusBadRequest)
			return
		}

		// log.Printf("收到登录凭据，用户名: %s", credentials.Username)

		// 验证用户名和密码
		usernameMatch := subtle.ConstantTimeCompare([]byte(credentials.Username), []byte(h.webConfig.Username)) == 1
		passwordMatch := subtle.ConstantTimeCompare([]byte(credentials.Password), []byte(h.webConfig.Password)) == 1

		// log.Printf("用户名匹配: %t, 密码匹配: %t", usernameMatch, passwordMatch)

		if h.webConfig.Enabled && usernameMatch && passwordMatch {
			// 认证成功，设置会话cookie
			// log.Printf("认证成功，设置认证Cookie")
			http.SetCookie(w, &http.Cookie{
				Name:     "tvgate_auth",
				Value:    h.generateAuthCookieValue(credentials.Username),
				Path:     webPath,
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
				MaxAge:   3600, // 1小时
			})

			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status": "success"}`))
			return
		}

		// 认证失败
		// log.Printf("认证失败")
		http.Error(w, "用户名或密码错误", http.StatusUnauthorized)
		return
	}

	// log.Printf("不支持的请求方法: %s", r.Method)
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// handleLogout 处理退出登录
func (h *ConfigHandler) handleLogout(w http.ResponseWriter, r *http.Request) {
	// log.Printf("处理退出请求: Path=%s, Method=%s", r.URL.Path, r.Method)

	if r.Method != http.MethodGet {
		// log.Printf("不支持的请求方法: %s", r.Method)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 清除认证cookie
	webPath := h.getWebPath()
	// log.Printf("清除认证Cookie，路径: %s", webPath)

	http.SetCookie(w, &http.Cookie{
		Name:   "tvgate_auth",
		Value:  "",
		Path:   webPath,
		MaxAge: -1,
	})

	// 重定向到登录页面
	redirectURL := webPath + "login"
	// log.Printf("重定向到登录页面: %s", redirectURL)
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// handleAuthStatus 处理认证状态检查
func (h *ConfigHandler) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	// log.Printf("处理认证状态检查请求: Path=%s, Method=%s", r.URL.Path, r.Method)

	if r.Method != http.MethodGet {
		// log.Printf("不支持的请求方法: %s", r.Method)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 检查用户是否已认证
	isAuthenticated := h.isAuthenticated(r)
	username := ""
	if isAuthenticated {
		if cookie, err := r.Cookie("tvgate_auth"); err == nil {
			username = h.getUsernameFromCookie(cookie.Value)
		}
	}

	// log.Printf("认证状态: %t, 用户名: %s", isAuthenticated, username)

	// 返回认证状态
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"authenticated": isAuthenticated,
		"username":      username,
	})
}

// isAuthenticated 检查用户是否已认证
func (h *ConfigHandler) isAuthenticated(r *http.Request) bool {
	// 如果Web管理未启用，认为用户已认证（向后兼容）
	if !h.webConfig.Enabled {
		// log.Printf("Web管理未启用，认为用户已认证")
		return true
	}

	// 检查cookie
	cookie, err := r.Cookie("tvgate_auth")
	if err != nil {
		// log.Printf("未找到认证Cookie: %v", err)
		return false
	}

	// log.Printf("找到认证Cookie，验证中...")
	result := h.validateAuthCookie(cookie.Value)
	// log.Printf("Cookie验证结果: %t", result)
	return result
}

// getWebPath 获取Web路径
func (h *ConfigHandler) getWebPath() string {
	webPath := h.webConfig.Path
	// log.Printf("配置中的Web路径: '%s'", webPath)

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

	// log.Printf("处理后的Web路径: '%s'", webPath)
	return webPath
}

// generateAuthCookieValue 生成认证cookie值
func (h *ConfigHandler) generateAuthCookieValue(username string) string {
	// 简单的认证机制：用户名+时间戳的哈希
	data := username + "|" + strconv.FormatInt(time.Now().Unix(), 10)
	// log.Printf("生成Cookie数据: %s", data)
	// log.Printf("使用密码: %s", h.webConfig.Password)
	hash := sha256.Sum256([]byte(data + h.webConfig.Password))
	// log.Printf("生成的哈希: %x", hash)
	return fmt.Sprintf("%s|%x", data, hash)
}

// validateAuthCookie 验证认证cookie值
func (h *ConfigHandler) validateAuthCookie(cookieValue string) bool {
	// log.Printf("验证Cookie值: %s", cookieValue)
	parts := strings.Split(cookieValue, "|")
	if len(parts) != 3 {
		// log.Printf("Cookie格式无效，部分数量不正确: %d", len(parts))
		return false
	}

	username := parts[0]
	timestamp := parts[1]
	hash := parts[2]

	// log.Printf("解析Cookie部分 - 用户名: %s, 时间戳: %s, 哈希: %s", username, timestamp, hash)

	// 验证时间戳（1小时内有效）
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		// log.Printf("解析时间戳失败: %v", err)
		return false
	}

	timeDiff := time.Now().Unix() - ts
	// log.Printf("时间差: %d秒", timeDiff)

	if timeDiff > 3600 {
		// log.Printf("Cookie已过期，时间差: %d秒", timeDiff)
		return false
	}

	if timeDiff < 0 {
		// log.Printf("Cookie时间在未来，时间差: %d秒", timeDiff)
		return false
	}

	// 验证哈希
	data := username + "|" + timestamp
	// log.Printf("用于验证的数据: %s", data)
	// log.Printf("配置中的密码: %s", h.webConfig.Password)
	expectedHash := sha256.Sum256([]byte(data + h.webConfig.Password))
	// log.Printf("期望的哈希: %x", expectedHash)
	// log.Printf("实际的哈希: %s", hash)

	// 将十六进制字符串转换为字节切片进行比较
	expectedHashBytes, err := hex.DecodeString(hash)
	if err != nil {
		// log.Printf("解析实际哈希失败: %v", err)
		return false
	}

	hashMatch := subtle.ConstantTimeCompare(expectedHashBytes, expectedHash[:]) == 1
	// log.Printf("哈希验证结果: %t", hashMatch)
	return hashMatch
}

// getUsernameFromCookie 从cookie值中提取用户名
func (h *ConfigHandler) getUsernameFromCookie(cookieValue string) string {
	parts := strings.Split(cookieValue, "|")
	if len(parts) >= 1 {
		return parts[0]
	}
	return ""
}

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

		// 渲染编辑器模板
		data := map[string]interface{}{
			"title":   "TVGate 配置编辑器",
			"webPath": webPath,
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

// handleGroupEditor 处理组编辑器页面请求
func (h *ConfigHandler) handleGroupEditor(w http.ResponseWriter, r *http.Request) {
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

	// 如果请求的是组编辑器路径
	if r.URL.Path == webPath+"group-editor" {
		// 只允许GET方法
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// 获取参数
		configType := r.URL.Query().Get("config")
		groupName := r.URL.Query().Get("group")

		// 设置模板数据
		data := map[string]interface{}{
			"webPath": webPath,
			"config":  configType,
			"group":   groupName,
		}

		// 如果提供了参数，则设置标题和参数
		if configType != "" && groupName != "" {
			data["title"] = fmt.Sprintf("配置组编辑器 - %s.%s", configType, groupName)
		} else {
			data["title"] = "组配置编辑器"
		}

		// 从嵌入的文件系统读取模板
		content, err := templatesFS.ReadFile("templates/group_editor.html")
		if err != nil {
			http.Error(w, "Failed to read template file: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 解析模板
		tmpl, err := template.New("group_editor").Parse(string(content))
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

// handleConfig 处理配置查看页面
func (h *ConfigHandler) handleConfig(w http.ResponseWriter, r *http.Request) {
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

	// 如果请求的是配置路径
	if r.URL.Path == webPath+"config" {
		// 读取配置文件内容
		configPath := *config.ConfigFilePath
		contentBytes, err := os.ReadFile(configPath)
		if err != nil {
			http.Error(w, "Failed to read config file: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 设置响应头为YAML格式
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write(contentBytes)
		return
	}

	http.NotFound(w, r)
}

// handleConfigSave 处理配置保存请求
func (h *ConfigHandler) handleConfigSave(w http.ResponseWriter, r *http.Request) {
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

	// 如果请求的是配置保存路径
	if r.URL.Path == webPath+"config/save" {
		// 只允许POST方法
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// 读取请求体中的配置内容，确保使用UTF-8编码
		content, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		// 验证YAML格式，使用yaml.Node保留注释
		var temp yaml.Node
		if err := yaml.Unmarshal(content, &temp); err != nil {
			http.Error(w, "YAML格式错误: "+err.Error(), http.StatusBadRequest)
			return
		}

		// 获取配置文件路径
		configPath := *config.ConfigFilePath

		// 备份当前配置文件
		backupPath := configPath + ".backup"
		if err := copyFile(configPath, backupPath); err != nil {
			http.Error(w, "Failed to create backup: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 尝试将新配置写入文件，确保使用正确的权限
		if err := os.WriteFile(configPath, content, 0644); err != nil {
			// 如果写入失败，尝试恢复备份
			os.Rename(backupPath, configPath)
			http.Error(w, "Failed to save config: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 备份文件保留，不删除
		// os.Remove(backupPath) // 注释掉这行，保留备份文件

		// 返回成功响应
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "success", "message": "Configuration saved successfully"}`))
		return
	}

	http.NotFound(w, r)
}

// handleConfigValidate 处理配置验证请求
func (h *ConfigHandler) handleConfigValidate(w http.ResponseWriter, r *http.Request) {
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

	// 如果请求的是配置验证路径
	if r.URL.Path == webPath+"config/validate" {
		// 只允许POST方法
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// 读取请求体中的配置内容
		content, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		// 验证YAML格式，使用yaml.Node保留注释
		var temp yaml.Node
		if err := yaml.Unmarshal(content, &temp); err != nil {
			http.Error(w, "YAML格式错误: "+err.Error(), http.StatusBadRequest)
			return
		}

		// 尝试解析为配置结构体以进行更深入的验证
		var newCfg config.Config
		if err := yaml.Unmarshal(content, &newCfg); err != nil {
			http.Error(w, "配置结构验证失败: "+err.Error(), http.StatusBadRequest)
			return
		}

		// 返回成功响应
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "success", "message": "Configuration validation passed"}`))
		return
	}

	http.NotFound(w, r)
}

// handleNodeConfig 处理节点配置获取请求
func (h *ConfigHandler) handleNodeConfig(w http.ResponseWriter, r *http.Request) {
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

	// 如果请求的是节点配置路径
	if r.URL.Path == webPath+"config/node" {
		// 只允许GET方法
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// 获取节点参数
		node := r.URL.Query().Get("node")
		if node == "" {
			http.Error(w, "Missing node parameter", http.StatusBadRequest)
			return
		}

		// 获取配置文件路径
		configPath := *config.ConfigFilePath

		// 读取完整配置文件
		fullConfigData, err := os.ReadFile(configPath)
		if err != nil {
			http.Error(w, "Failed to read config file: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 解析完整配置并保留注释
		var fullNode yaml.Node
		if err := yaml.Unmarshal(fullConfigData, &fullNode); err != nil {
			http.Error(w, "Failed to parse config file: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 查找指定节点
		var nodeContent *yaml.Node
		if fullNode.Kind == yaml.DocumentNode && len(fullNode.Content) > 0 {
			doc := fullNode.Content[0]
			if doc.Kind == yaml.MappingNode {
				// 遍历映射节点查找指定的键
				for i := 0; i < len(doc.Content); i += 2 {
					keyNode := doc.Content[i]
					valueNode := doc.Content[i+1]

					if keyNode.Kind == yaml.ScalarNode && keyNode.Value == node {
						nodeContent = valueNode
						break
					}
				}
			}
		}

		if nodeContent == nil {
			// 如果节点不存在，返回空内容而不是空节点
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(""))
			return
		}

		// 特殊处理JX节点 - 包含api_groups
		if node == "jx" {
			// 序列化整个JX节点（包含api_groups）
			nodeDataYAML, err := yaml.Marshal(nodeContent)
			if err != nil {
				http.Error(w, "Failed to serialize node data: "+err.Error(), http.StatusInternalServerError)
				return
			}

			// 返回节点配置
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			w.Write(nodeDataYAML)
			return
		}

		// 特殊处理ProxyGroups节点
		if node == "proxygroups" {
			// 序列化整个ProxyGroups节点
			nodeDataYAML, err := yaml.Marshal(nodeContent)
			if err != nil {
				http.Error(w, "Failed to serialize node data: "+err.Error(), http.StatusInternalServerError)
				return
			}

			// 返回节点配置
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			w.Write(nodeDataYAML)
			return
		}

		// 序列化节点数据（保留注释）
		nodeDataYAML, err := yaml.Marshal(nodeContent)
		if err != nil {
			http.Error(w, "Failed to serialize node data: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 返回节点配置
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write(nodeDataYAML)
		return
	}

	http.NotFound(w, r)
}

// handleConfigSaveNode 处理配置节点保存请求
func (h *ConfigHandler) handleConfigSaveNode(w http.ResponseWriter, r *http.Request) {
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

	// 如果请求的是配置节点保存路径
	if r.URL.Path == webPath+"config/save-node" {
		// 只允许POST方法
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// 获取节点参数
		node := r.URL.Query().Get("node")
		if node == "" {
			http.Error(w, "Missing node parameter", http.StatusBadRequest)
			return
		}

		// 读取请求体中的配置内容
		content, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		// 验证YAML格式，使用yaml.Node保留注释
		var temp yaml.Node
		if err := yaml.Unmarshal(content, &temp); err != nil {
			http.Error(w, "YAML格式错误: "+err.Error(), http.StatusBadRequest)
			return
		}

		// 获取配置文件路径
		configPath := *config.ConfigFilePath

		// 读取完整配置文件
		fullConfigData, err := os.ReadFile(configPath)
		if err != nil {
			http.Error(w, "Failed to read config file: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 解析完整配置并保留注释
		var fullNode yaml.Node
		if err := yaml.Unmarshal(fullConfigData, &fullNode); err != nil {
			http.Error(w, "Failed to parse config file: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 解析新节点内容
		var newNode yaml.Node
		if err := yaml.Unmarshal(content, &newNode); err != nil {
			http.Error(w, "Failed to parse node data: "+err.Error(), http.StatusBadRequest)
			return
		}

		// 查找并替换指定节点
		replaced := false
		if fullNode.Kind == yaml.DocumentNode && len(fullNode.Content) > 0 {
			doc := fullNode.Content[0]
			if doc.Kind == yaml.MappingNode {
				// 遍历映射节点查找并替换指定的键
				for i := 0; i < len(doc.Content); i += 2 {
					keyNode := doc.Content[i]

					if keyNode.Kind == yaml.ScalarNode && keyNode.Value == node {
						// 替换值节点
						doc.Content[i+1] = newNode.Content[0]
						replaced = true
						break
					}
				}

				// 如果节点不存在，添加新节点
				if !replaced {
					keyNode := &yaml.Node{
						Kind:  yaml.ScalarNode,
						Value: node,
					}
					doc.Content = append(doc.Content, keyNode, newNode.Content[0])
					replaced = true
				}
			}
		}

		if !replaced {
			http.Error(w, "Failed to update node", http.StatusInternalServerError)
			return
		}

		// 重新序列化完整配置（保留注释）
		newConfigData, err := yaml.Marshal(&fullNode)
		if err != nil {
			http.Error(w, "Failed to serialize config: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 备份当前配置文件
		backupPath := configPath + ".backup"
		if err := copyFile(configPath, backupPath); err != nil {
			http.Error(w, "Failed to create backup: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 尝试将新配置写入文件
		if err := os.WriteFile(configPath, newConfigData, 0644); err != nil {
			// 如果写入失败，尝试恢复备份
			os.Rename(backupPath, configPath)
			http.Error(w, "Failed to save config: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 备份文件保留，不删除
		// os.Remove(backupPath) // 注释掉这行，保留备份文件

		// 返回成功响应
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "success", "message": "Configuration node saved successfully"}`))
		return
	}

	http.NotFound(w, r)
}

// handleGroupConfig 处理组配置获取请求
func (h *ConfigHandler) handleGroupConfig(w http.ResponseWriter, r *http.Request) {
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

	// 如果请求的是组配置路径
	if r.URL.Path == webPath+"config/group" {
		// 只允许GET方法
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// 获取参数
		configType := r.URL.Query().Get("config")
		groupName := r.URL.Query().Get("group")

		if configType == "" || groupName == "" {
			http.Error(w, "Missing config or group parameter", http.StatusBadRequest)
			return
		}

		// 获取配置文件路径
		configPath := *config.ConfigFilePath

		// 读取完整配置文件
		fullConfigData, err := os.ReadFile(configPath)
		if err != nil {
			http.Error(w, "Failed to read config file: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 解析完整配置并保留注释
		var fullNode yaml.Node
		if err := yaml.Unmarshal(fullConfigData, &fullNode); err != nil {
			http.Error(w, "Failed to parse config file: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 查找配置类型节点
		var configContent *yaml.Node
		if fullNode.Kind == yaml.DocumentNode && len(fullNode.Content) > 0 {
			doc := fullNode.Content[0]
			if doc.Kind == yaml.MappingNode {
				// 遍历映射节点查找指定的配置类型
				for i := 0; i < len(doc.Content); i += 2 {
					keyNode := doc.Content[i]
					valueNode := doc.Content[i+1]

					if keyNode.Kind == yaml.ScalarNode && keyNode.Value == configType {
						configContent = valueNode
						break
					}
				}
			}
		}

		if configContent == nil {
			http.Error(w, "Config type not found", http.StatusNotFound)
			return
		}

		// 查找组节点
		var groupContent *yaml.Node
		// 特殊处理JX配置 - 需要在api_groups子节点中查找
		if configType == "jx" {
			if configContent.Kind == yaml.MappingNode {
				// 查找api_groups节点
				var apiGroupsNode *yaml.Node
				for i := 0; i < len(configContent.Content); i += 2 {
					keyNode := configContent.Content[i]
					valueNode := configContent.Content[i+1]

					if keyNode.Kind == yaml.ScalarNode && keyNode.Value == "api_groups" {
						apiGroupsNode = valueNode
						break
					}
				}

				if apiGroupsNode != nil && apiGroupsNode.Kind == yaml.MappingNode {
					// 在api_groups中查找指定组
					for i := 0; i < len(apiGroupsNode.Content); i += 2 {
						keyNode := apiGroupsNode.Content[i]
						valueNode := apiGroupsNode.Content[i+1]

						if keyNode.Kind == yaml.ScalarNode && keyNode.Value == groupName {
							groupContent = valueNode
							break
						}
					}
				}
			}
		} else {
			// 其他配置类型直接在配置内容中查找组
			if configContent.Kind == yaml.MappingNode {
				// 遍历映射节点查找指定的组
				for i := 0; i < len(configContent.Content); i += 2 {
					keyNode := configContent.Content[i]
					valueNode := configContent.Content[i+1]

					if keyNode.Kind == yaml.ScalarNode && keyNode.Value == groupName {
						groupContent = valueNode
						break
					}
				}
			}
		}

		if groupContent == nil {
			http.Error(w, "Group not found", http.StatusNotFound)
			return
		}

		// 序列化组数据（保留注释）
		groupDataYAML, err := yaml.Marshal(groupContent)
		if err != nil {
			http.Error(w, "Failed to serialize group data: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 返回组配置
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write(groupDataYAML)
		return
	}

	http.NotFound(w, r)
}

// handleConfigSaveGroup 处理代理组配置保存请求
func (h *ConfigHandler) handleConfigSaveGroup(w http.ResponseWriter, r *http.Request) {
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

	// 如果请求的是配置组保存路径
	if r.URL.Path == webPath+"config/save-group" {
		// 只允许POST方法
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// 获取参数
		configType := r.URL.Query().Get("config")
		groupName := r.URL.Query().Get("group")

		if configType == "" || groupName == "" {
			http.Error(w, "Missing config or group parameter", http.StatusBadRequest)
			return
		}

		// 读取请求体中的配置内容
		content, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		// 验证YAML格式，使用yaml.Node保留注释
		var temp yaml.Node
		if err := yaml.Unmarshal(content, &temp); err != nil {
			http.Error(w, "YAML格式错误: "+err.Error(), http.StatusBadRequest)
			return
		}

		// 获取配置文件路径
		configPath := *config.ConfigFilePath

		// 读取完整配置文件
		fullConfigData, err := os.ReadFile(configPath)
		if err != nil {
			http.Error(w, "Failed to read config file: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 解析完整配置并保留注释
		var fullNode yaml.Node
		if err := yaml.Unmarshal(fullConfigData, &fullNode); err != nil {
			http.Error(w, "Failed to parse config file: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 解析新组内容
		var newNode yaml.Node
		if err := yaml.Unmarshal(content, &newNode); err != nil {
			http.Error(w, "Failed to parse group data: "+err.Error(), http.StatusBadRequest)
			return
		}

		// 查找配置类型节点
		var configContent *yaml.Node
		configFound := false
		if fullNode.Kind == yaml.DocumentNode && len(fullNode.Content) > 0 {
			doc := fullNode.Content[0]
			if doc.Kind == yaml.MappingNode {
				// 遍历映射节点查找指定的配置类型
				for i := 0; i < len(doc.Content); i += 2 {
					keyNode := doc.Content[i]
					valueNode := doc.Content[i+1]

					if keyNode.Kind == yaml.ScalarNode && keyNode.Value == configType {
						configContent = valueNode
						configFound = true
						break
					}
				}

				// 如果配置类型不存在，创建一个新的映射节点
				if !configFound {
					keyNode := &yaml.Node{
						Kind:  yaml.ScalarNode,
						Value: configType,
					}
					configContent = &yaml.Node{
						Kind: yaml.MappingNode,
					}
					doc.Content = append(doc.Content, keyNode, configContent)
					configFound = true
				}
			}
		}

		if !configFound {
			http.Error(w, "Failed to find or create config type", http.StatusInternalServerError)
			return
		}

		// 确保配置内容是映射节点
		if configContent.Kind != yaml.MappingNode {
			configContent.Kind = yaml.MappingNode
			configContent.Content = make([]*yaml.Node, 0)
		}

		// 特殊处理JX配置 - 需要更新api_groups子节点
		if configType == "jx" {
			// 对于JX配置，需要查找或创建api_groups节点，然后在其中更新组
			var apiGroupsNode *yaml.Node
			apiGroupsFound := false

			// 查找api_groups节点
			for i := 0; i < len(configContent.Content); i += 2 {
				keyNode := configContent.Content[i]
				valueNode := configContent.Content[i+1]

				if keyNode.Kind == yaml.ScalarNode && keyNode.Value == "api_groups" {
					apiGroupsNode = valueNode
					apiGroupsFound = true
					break
				}
			}

			// 如果api_groups节点不存在，创建一个新的映射节点
			if !apiGroupsFound {
				keyNode := &yaml.Node{
					Kind:  yaml.ScalarNode,
					Value: "api_groups",
				}
				apiGroupsNode = &yaml.Node{
					Kind: yaml.MappingNode,
				}
				configContent.Content = append(configContent.Content, keyNode, apiGroupsNode)
				apiGroupsFound = true
			}

			// 确保api_groups节点是映射节点
			if apiGroupsNode.Kind != yaml.MappingNode {
				apiGroupsNode.Kind = yaml.MappingNode
				apiGroupsNode.Content = make([]*yaml.Node, 0)
			}

			// 查找并替换指定组
			replaced := false
			for i := 0; i < len(apiGroupsNode.Content); i += 2 {
				keyNode := apiGroupsNode.Content[i]

				if keyNode.Kind == yaml.ScalarNode && keyNode.Value == groupName {
					// 替换值节点
					apiGroupsNode.Content[i+1] = newNode.Content[0]
					replaced = true
					break
				}
			}

			// 如果组不存在，添加新组
			if !replaced {
				keyNode := &yaml.Node{
					Kind:  yaml.ScalarNode,
					Value: groupName,
				}
				apiGroupsNode.Content = append(apiGroupsNode.Content, keyNode, newNode.Content[0])
				replaced = true
			}
		} else {
			// 其他配置类型直接在配置内容中更新组
			// 查找并替换指定组
			replaced := false
			for i := 0; i < len(configContent.Content); i += 2 {
				keyNode := configContent.Content[i]

				if keyNode.Kind == yaml.ScalarNode && keyNode.Value == groupName {
					// 替换值节点
					configContent.Content[i+1] = newNode.Content[0]
					replaced = true
					break
				}
			}

			// 如果组不存在，添加新组
			if !replaced {
				keyNode := &yaml.Node{
					Kind:  yaml.ScalarNode,
					Value: groupName,
				}
				configContent.Content = append(configContent.Content, keyNode, newNode.Content[0])
				replaced = true
			}
		}

		// 重新序列化完整配置（保留注释）
		newConfigData, err := yaml.Marshal(&fullNode)
		if err != nil {
			http.Error(w, "Failed to serialize config: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 备份当前配置文件
		backupPath := configPath + ".backup"
		if err := copyFile(configPath, backupPath); err != nil {
			http.Error(w, "Failed to create backup: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 尝试将新配置写入文件
		if err := os.WriteFile(configPath, newConfigData, 0644); err != nil {
			// 如果写入失败，尝试恢复备份
			os.Rename(backupPath, configPath)
			http.Error(w, "Failed to save config: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 备份文件保留，不删除
		// os.Remove(backupPath) // 注释掉这行，保留备份文件

		// 返回成功响应
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "success", "message": "Configuration group saved successfully"}`))
		return
	}

	http.NotFound(w, r)
}

// handleDomainMapEditor 处理域名映射编辑器页面
func (h *ConfigHandler) handleDomainMapEditor(w http.ResponseWriter, r *http.Request) {
	webPath := h.getWebPath()

	data := map[string]interface{}{
		"title":   "TVGate 域名映射编辑器",
		"webPath": webPath,
	}

	if err := h.renderTemplate(w, r, "domainmap_editor", "templates/domainmap_editor.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleGlobalAuthEditor 处理全局认证编辑器页面
func (h *ConfigHandler) handleGlobalAuthEditor(w http.ResponseWriter, r *http.Request) {
	webPath := h.getWebPath()

	data := map[string]interface{}{
		"title":   "TVGate 全局认证编辑器",
		"webPath": webPath,
	}

	if err := h.renderTemplate(w, r, "global_auth_editor", "templates/global_auth_editor.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleDomainMapConfig 处理域名映射配置获取请求
func (h *ConfigHandler) handleDomainMapConfig(w http.ResponseWriter, r *http.Request) {
	// 设置响应头
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	// 获取当前配置
	config.CfgMu.RLock()
	domainMaps := config.Cfg.DomainMap
	config.CfgMu.RUnlock()

	// 转换为可JSON序列化的格式
	domainMapList := make([]map[string]interface{}, len(domainMaps))
	for i, dm := range domainMaps {
		dmMap := map[string]interface{}{
			"name":     dm.Name,
			"source":   dm.Source,
			"target":   dm.Target,
			"protocol": dm.Protocol,
		}

		// 添加auth配置（如果存在）
		if dm.Auth.TokensEnabled ||
			dm.Auth.TokenParamName != "" ||
			dm.Auth.DynamicTokens.EnableDynamic ||
			dm.Auth.DynamicTokens.Secret != "" ||
			dm.Auth.DynamicTokens.Salt != "" ||
			dm.Auth.StaticTokens.EnableStatic ||
			dm.Auth.StaticTokens.Token != "" {

			dmMap["auth"] = map[string]interface{}{
				"tokens_enabled":   dm.Auth.TokensEnabled,
				"token_param_name": dm.Auth.TokenParamName,
				"dynamic_tokens": map[string]interface{}{
					"enable_dynamic": dm.Auth.DynamicTokens.EnableDynamic,
					"dynamic_ttl":    formatDuration(dm.Auth.DynamicTokens.DynamicTTL),
					"secret":         dm.Auth.DynamicTokens.Secret,
					"salt":           dm.Auth.DynamicTokens.Salt,
				},
				"static_tokens": map[string]interface{}{
					"enable_static": dm.Auth.StaticTokens.EnableStatic,
					"token":         dm.Auth.StaticTokens.Token,
					"expire_hours":  formatDuration(dm.Auth.StaticTokens.ExpireHours),
				},
			}
		}

		domainMapList[i] = dmMap
	}

	// 返回JSON格式的配置
	if err := json.NewEncoder(w).Encode(domainMapList); err != nil {
		http.Error(w, "序列化配置失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
}

// handleGlobalAuthConfig 处理全局认证配置获取请求
func (h *ConfigHandler) handleGlobalAuthConfig(w http.ResponseWriter, r *http.Request) {
	// 设置响应头
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	// 获取当前配置
	config.CfgMu.RLock()
	globalAuth := config.Cfg.GlobalAuth
	config.CfgMu.RUnlock()

	// 转换为可JSON序列化的格式
	authConfig := map[string]interface{}{
		"tokens_enabled":   globalAuth.TokensEnabled,
		"token_param_name": globalAuth.TokenParamName,
		"dynamic_tokens": map[string]interface{}{
			"enable_dynamic": globalAuth.DynamicTokens.EnableDynamic,
			"dynamic_ttl":    formatDuration(globalAuth.DynamicTokens.DynamicTTL),
			"secret":         globalAuth.DynamicTokens.Secret,
			"salt":           globalAuth.DynamicTokens.Salt,
		},
		"static_tokens": map[string]interface{}{
			"enable_static": globalAuth.StaticTokens.EnableStatic,
			"token":         globalAuth.StaticTokens.Token,
			"expire_hours":  formatDuration(globalAuth.StaticTokens.ExpireHours),
		},
	}

	// 返回JSON格式的配置
	if err := json.NewEncoder(w).Encode(authConfig); err != nil {
		http.Error(w, "序列化配置失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
}

// handleDomainMapConfigSave 处理域名映射配置保存请求
func (h *ConfigHandler) handleDomainMapConfigSave(w http.ResponseWriter, r *http.Request) {
	// 检查请求方法
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}

	// 读取请求体
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "读取请求体失败: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// 解析JSON数据
	var domainMaps []map[string]interface{}
	if err := json.Unmarshal(body, &domainMaps); err != nil {
		http.Error(w, "解析JSON失败: "+err.Error(), http.StatusBadRequest)
		return
	}

	// 读取配置文件
	configPath := *config.ConfigFilePath
	data, err := os.ReadFile(configPath)
	if err != nil {
		http.Error(w, "读取配置文件失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 使用yaml.Node解析YAML配置以保持注释和格式
	var fullNode yaml.Node
	if err := yaml.Unmarshal(data, &fullNode); err != nil {
		http.Error(w, "解析配置文件失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 转换域名映射数据
	yamlDomainMaps := make([]*yaml.Node, len(domainMaps))
	for i, dm := range domainMaps {
		domainMapNode := &yaml.Node{Kind: yaml.MappingNode}

		// 添加基本字段
		domainMapNode.Content = append(domainMapNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "name"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", dm["name"])},
			&yaml.Node{Kind: yaml.ScalarNode, Value: "source"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", dm["source"])},
			&yaml.Node{Kind: yaml.ScalarNode, Value: "target"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", dm["target"])},
		)

		// 添加协议字段（如果存在且非空）
		if protocol, ok := dm["protocol"]; ok && protocol != "" && protocol != nil {
			domainMapNode.Content = append(domainMapNode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "protocol"},
				&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", protocol)},
			)
		}

		// 添加auth字段（如果存在）
		if auth, ok := dm["auth"]; ok && auth != nil {
			if authMap, ok := auth.(map[string]interface{}); ok {
				authNode := &yaml.Node{Kind: yaml.MappingNode}

				// 添加tokens_enabled
				if tokensEnabled, ok := authMap["tokens_enabled"]; ok {
					authNode.Content = append(authNode.Content,
						&yaml.Node{Kind: yaml.ScalarNode, Value: "tokens_enabled"},
						&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", tokensEnabled)},
					)
				}

				// 添加token_param_name
				if tokenParamName, ok := authMap["token_param_name"]; ok && tokenParamName != "" {
					authNode.Content = append(authNode.Content,
						&yaml.Node{Kind: yaml.ScalarNode, Value: "token_param_name"},
						&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", tokenParamName)},
					)
				}

				// 添加dynamic_tokens
				if dynamicTokens, ok := authMap["dynamic_tokens"]; ok {
					if dtMap, ok := dynamicTokens.(map[string]interface{}); ok {
						dtNode := &yaml.Node{Kind: yaml.MappingNode}

						if enableDynamic, ok := dtMap["enable_dynamic"]; ok {
							dtNode.Content = append(dtNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "enable_dynamic"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", enableDynamic)},
							)
						}

						if dynamicTTL, ok := dtMap["dynamic_ttl"]; ok && dynamicTTL != "" {
							// 解析时间字符串
							if ttlStr, ok := dynamicTTL.(string); ok && ttlStr != "" {
								// 格式化时间字符串
								formattedTTL := formatDurationString(ttlStr)
								if _, err := time.ParseDuration(formattedTTL); err == nil {
									dtNode.Content = append(dtNode.Content,
										&yaml.Node{Kind: yaml.ScalarNode, Value: "dynamic_ttl"},
										&yaml.Node{Kind: yaml.ScalarNode, Value: formattedTTL},
									)
								}
							}
						}

						if secret, ok := dtMap["secret"]; ok && secret != "" {
							dtNode.Content = append(dtNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "secret"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", secret)},
							)
						}

						if salt, ok := dtMap["salt"]; ok && salt != "" {
							dtNode.Content = append(dtNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "salt"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", salt)},
							)
						}

						if len(dtNode.Content) > 0 {
							authNode.Content = append(authNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "dynamic_tokens"},
								dtNode,
							)
						}
					}
				}

				// 添加static_tokens
				if staticTokens, ok := authMap["static_tokens"]; ok {
					if stMap, ok := staticTokens.(map[string]interface{}); ok {
						stNode := &yaml.Node{Kind: yaml.MappingNode}

						if enableStatic, ok := stMap["enable_static"]; ok {
							stNode.Content = append(stNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "enable_static"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", enableStatic)},
							)
						}

						if token, ok := stMap["token"]; ok && token != "" {
							stNode.Content = append(stNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "token"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", token)},
							)
						}

						if expireHours, ok := stMap["expire_hours"]; ok && expireHours != "" {
							// 解析时间字符串
							if expireStr, ok := expireHours.(string); ok && expireStr != "" {
								// 格式化时间字符串
								formattedExpire := formatDurationString(expireStr)
								if _, err := time.ParseDuration(formattedExpire); err == nil {
									stNode.Content = append(stNode.Content,
										&yaml.Node{Kind: yaml.ScalarNode, Value: "expire_hours"},
										&yaml.Node{Kind: yaml.ScalarNode, Value: formattedExpire},
									)
								}
							}
						}

						if len(stNode.Content) > 0 {
							authNode.Content = append(authNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "static_tokens"},
								stNode,
							)
						}
					}
				}

				if len(authNode.Content) > 0 {
					domainMapNode.Content = append(domainMapNode.Content,
						&yaml.Node{Kind: yaml.ScalarNode, Value: "auth"},
						authNode,
					)
				}
			}
		}

		yamlDomainMaps[i] = domainMapNode
	}

	// 更新域名映射配置，保持原有结构
	if fullNode.Kind == yaml.DocumentNode && len(fullNode.Content) > 0 {
		doc := fullNode.Content[0]
		if doc.Kind == yaml.MappingNode {
			// 查找domainmap节点并更新
			for i := 0; i < len(doc.Content); i += 2 {
				keyNode := doc.Content[i]
				if keyNode.Kind == yaml.ScalarNode && keyNode.Value == "domainmap" {
					// 创建新的序列节点
					seqNode := &yaml.Node{Kind: yaml.SequenceNode}
					seqNode.Content = yamlDomainMaps
					// 替换值节点
					doc.Content[i+1] = seqNode
					break
				}
			}
		}
	}

	// 序列化为YAML格式
	newData, err := yaml.Marshal(&fullNode)
	if err != nil {
		http.Error(w, "序列化配置失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 创建备份文件
	backupPath := configPath + ".backup." + time.Now().Format("20060102150405")
	if err := os.WriteFile(backupPath, data, 0644); err != nil {
		http.Error(w, "创建备份文件失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 写入新配置
	if err := os.WriteFile(configPath, newData, 0644); err != nil {
		// 恢复备份文件
		os.WriteFile(configPath, data, 0644)
		http.Error(w, "写入配置文件失败，已恢复备份: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 返回成功响应
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("配置保存成功"))
}

// formatDurationString 格式化时间字符串，去除末尾的0部分
func formatDurationString(durationStr string) string {
	if durationStr == "" {
		return ""
	}

	// 移除末尾的0部分
	result := durationStr
	// 移除末尾的0s
	result = strings.TrimSuffix(result, "0s")
	// 移除末尾的0m
	result = strings.TrimSuffix(result, "0m")
	// 移除末尾的0h
	result = strings.TrimSuffix(result, "0h")
	// 处理特殊情况，如1m0s变成1m
	result = strings.ReplaceAll(result, "m0s", "m")
	// 处理特殊情况，如1h0m变成1h
	result = strings.ReplaceAll(result, "h0m", "h")
	// 处理特殊情况，如1h0s变成1h
	result = strings.ReplaceAll(result, "h0s", "h")

	return result
}

// handleGlobalAuthConfigSave 处理全局认证配置保存请求
func (h *ConfigHandler) handleGlobalAuthConfigSave(w http.ResponseWriter, r *http.Request) {
	// 检查请求方法
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}

	// 读取请求体
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "读取请求体失败: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// 解析JSON数据
	var authConfig map[string]interface{}
	if err := json.Unmarshal(body, &authConfig); err != nil {
		http.Error(w, "解析JSON失败: "+err.Error(), http.StatusBadRequest)
		return
	}

	// 读取配置文件
	configPath := *config.ConfigFilePath
	data, err := os.ReadFile(configPath)
	if err != nil {
		http.Error(w, "读取配置文件失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 使用yaml.Node解析YAML配置以保持注释和格式
	var fullNode yaml.Node
	if err := yaml.Unmarshal(data, &fullNode); err != nil {
		http.Error(w, "解析配置文件失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 查找并更新global_auth节点
	if fullNode.Kind == yaml.DocumentNode && len(fullNode.Content) > 0 {
		doc := fullNode.Content[0]
		if doc.Kind == yaml.MappingNode {
			// 查找global_auth节点
			globalAuthFound := false
			for i := 0; i < len(doc.Content); i += 2 {
				keyNode := doc.Content[i]
				if keyNode.Kind == yaml.ScalarNode && keyNode.Value == "global_auth" {
					// 创建新的global_auth节点
					newAuthNode := &yaml.Node{Kind: yaml.MappingNode}

					// 添加tokens_enabled
					if tokensEnabled, ok := authConfig["tokens_enabled"]; ok {
						newAuthNode.Content = append(newAuthNode.Content,
							&yaml.Node{Kind: yaml.ScalarNode, Value: "tokens_enabled"},
							&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", tokensEnabled)})
					}

					// 添加token_param_name
					if tokenParamName, ok := authConfig["token_param_name"]; ok && tokenParamName != "" {
						newAuthNode.Content = append(newAuthNode.Content,
							&yaml.Node{Kind: yaml.ScalarNode, Value: "token_param_name"},
							&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", tokenParamName)})
					}

					// 添加dynamic_tokens
					if dynamicTokens, ok := authConfig["dynamic_tokens"]; ok {
						if dtMap, ok := dynamicTokens.(map[string]interface{}); ok {
							dtNode := &yaml.Node{Kind: yaml.MappingNode}

							if enableDynamic, ok := dtMap["enable_dynamic"]; ok {
								dtNode.Content = append(dtNode.Content,
									&yaml.Node{Kind: yaml.ScalarNode, Value: "enable_dynamic"},
									&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", enableDynamic)})
							}

							if dynamicTTL, ok := dtMap["dynamic_ttl"]; ok && dynamicTTL != "" {
								// 解析时间字符串
								if ttlStr, ok := dynamicTTL.(string); ok && ttlStr != "" {
									// 格式化时间字符串
									formattedTTL := formatDurationString(ttlStr)
									if _, err := time.ParseDuration(formattedTTL); err == nil {
										dtNode.Content = append(dtNode.Content,
											&yaml.Node{Kind: yaml.ScalarNode, Value: "dynamic_ttl"},
											&yaml.Node{Kind: yaml.ScalarNode, Value: formattedTTL})
									}
								}
							}

							if secret, ok := dtMap["secret"]; ok && secret != "" {
								dtNode.Content = append(dtNode.Content,
									&yaml.Node{Kind: yaml.ScalarNode, Value: "secret"},
									&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", secret)})
							}

							if salt, ok := dtMap["salt"]; ok && salt != "" {
								dtNode.Content = append(dtNode.Content,
									&yaml.Node{Kind: yaml.ScalarNode, Value: "salt"},
									&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", salt)})
							}

							if len(dtNode.Content) > 0 {
								newAuthNode.Content = append(newAuthNode.Content,
									&yaml.Node{Kind: yaml.ScalarNode, Value: "dynamic_tokens"},
									dtNode)
							}
						}
					}

					// 添加static_tokens
					if staticTokens, ok := authConfig["static_tokens"]; ok {
						if stMap, ok := staticTokens.(map[string]interface{}); ok {
							stNode := &yaml.Node{Kind: yaml.MappingNode}

							if enableStatic, ok := stMap["enable_static"]; ok {
								stNode.Content = append(stNode.Content,
									&yaml.Node{Kind: yaml.ScalarNode, Value: "enable_static"},
									&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", enableStatic)})
							}

							if token, ok := stMap["token"]; ok && token != "" {
								stNode.Content = append(stNode.Content,
									&yaml.Node{Kind: yaml.ScalarNode, Value: "token"},
									&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", token)})
							}

							if expireHours, ok := stMap["expire_hours"]; ok && expireHours != "" {
								// 解析时间字符串
								if expireStr, ok := expireHours.(string); ok && expireStr != "" {
									// 格式化时间字符串
									formattedExpire := formatDurationString(expireStr)
									if _, err := time.ParseDuration(formattedExpire); err == nil {
										stNode.Content = append(stNode.Content,
											&yaml.Node{Kind: yaml.ScalarNode, Value: "expire_hours"},
											&yaml.Node{Kind: yaml.ScalarNode, Value: formattedExpire})
									}
								}
							}

							if len(stNode.Content) > 0 {
								newAuthNode.Content = append(newAuthNode.Content,
									&yaml.Node{Kind: yaml.ScalarNode, Value: "static_tokens"},
									stNode)
							}
						}
					}

					// 替换global_auth节点
					doc.Content[i+1] = newAuthNode
					globalAuthFound = true
					break
				}
			}

			// 如果没有找到global_auth节点，则创建一个新的
			if !globalAuthFound {
				doc.Content = append(doc.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "global_auth"},
					&yaml.Node{Kind: yaml.MappingNode})
			}
		}
	}

	// 序列化为YAML格式
	newData, err := yaml.Marshal(&fullNode)
	if err != nil {
		http.Error(w, "序列化配置失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 创建备份文件
	backupPath := configPath + ".backup." + time.Now().Format("20060102150405")
	if err := os.WriteFile(backupPath, data, 0644); err != nil {
		http.Error(w, "创建备份文件失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 写入新配置
	if err := os.WriteFile(configPath, newData, 0644); err != nil {
		// 恢复备份文件
		os.WriteFile(configPath, data, 0644)
		http.Error(w, "写入配置文件失败，已恢复备份: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 返回成功响应
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("配置保存成功"))
}

// formatDuration 格式化时间持续时间，如果为0则返回空字符串
func formatDuration(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return d.String()
}

// copyFile 复制文件的辅助函数
func copyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destination.Close()

	_, err = io.Copy(destination, source)
	return err
}
