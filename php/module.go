package php

import (
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/qist/tvgate/auth"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/phpgo"
	utilshttp "github.com/qist/tvgate/utils/http"
)

func init() {
	// 注册 Go mime 表中缺失的多媒体/文本扩展名，
	// 使 http.ServeFile 返回正确的 Content-Type。
	// 这样 jar/txt/js/json/xml/md/py/html/htm/jpg/jpeg/png/m3u 等静态文件
	// 浏览器会根据类型在线打开或下载。
	for ext, ct := range map[string]string{
		".m3u":  "audio/mpegurl",
		".m3u8": "application/vnd.apple.mpegurl",
		".ts":   "video/mp2t",
		".key":  "application/octet-stream",
		".crt":  "application/x-x509-ca-cert",
		".pem":  "application/x-pem-file",
		".jar":  "application/java-archive",
		".py":   "text/x-python; charset=utf-8",
		".md":   "text/markdown; charset=utf-8",
		".log":  "text/plain; charset=utf-8",
		".csv":  "text/csv; charset=utf-8",
		".svg":  "image/svg+xml",
		".webp": "image/webp",
		".ico":  "image/x-icon",
		".woff": "font/woff",
		".woff2": "font/woff2",
		".ttf":  "font/ttf",
		".wasm": "application/wasm",
	} {
		_ = mime.AddExtensionType(ext, ct)
	}
}

var (
	cfg     *config.PHPConfig
	client  *http.Client
	docRoot string
)

// Init 初始化纯 Go PHP 模块。
// 构建统一单二进制：从磁盘读取 cfg.PHP.DocRoot（可配置，默认 www，相对配置文件所在目录）脚本，
// 由 phpgo 解释器执行。复用 TVGate 的 HTTP client（含 DNS/代理能力）。
func Init(c *config.Config) error {
	cfg = &c.PHP
	// 仅在 client 未初始化时创建（首次调用）；热加载时复用已有 client
	if client == nil {
		client = utilshttp.NewHTTPClient(c, nil)
	}
	docRoot = cfg.DocRoot
	if docRoot == "" {
		// 兜底与配置默认一致：相对路径（相对配置文件所在目录）
		docRoot = "www"
	}
	return nil
}

// Shutdown 释放资源（目前无状态，预留接口）
func Shutdown() {}

// Handler 返回纯 Go PHP 的 HTTP 处理器。
// 映射：GET/POST 参数 -> $_GET/$_POST/$_COOKIE/$_SERVER，php://input 注入请求体，
// 执行磁盘脚本并将 echo/header/exit 映射为 HTTP 响应。
func Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		clientIP := r.RemoteAddr
		if h, _, err := net.SplitHostPort(clientIP); err == nil {
			clientIP = h
		}
		connID := clientIP + "_" + r.URL.Path

		// 全局 token 验证（与 HTTP/UDP/RTSP handler 一致）
		if gt := auth.GetGlobalTokenManager(); gt != nil {
			tokenParam := "my_token"
			if gt.TokenParamName != "" {
				tokenParam = gt.TokenParamName
			}
			token := r.URL.Query().Get(tokenParam)
			if !gt.ValidateToken(token, r.URL.Path, connID) {
				w.WriteHeader(http.StatusForbidden)
				logger.LogPHPRequest(r, r.URL.Path, http.StatusForbidden, 0)
				return
			}
			gt.KeepAlive(token, connID, clientIP, r.URL.Path)
			// 删除 token 参数，避免传到 PHP 脚本的 $_GET / $_POST / QUERY_STRING
			query := r.URL.Query()
			query.Del(tokenParam)
			r.URL.RawQuery = query.Encode()
		}

		// 解析脚本路径（防目录穿越）。先剥掉路由前缀（如 /php/）
		p := r.URL.Path
		if cfg.Path != "" {
			p = strings.TrimPrefix(p, cfg.Path)
		}
		rel := strings.Trim(p, "/")
		// rel 为空时访问 docroot 根目录（后面由目录逻辑处理 index 查找）
		scriptPath := filepath.Join(docRoot, rel)
		// 防穿越
		if !strings.HasPrefix(scriptPath, docRoot) {
			w.WriteHeader(http.StatusForbidden)
			logger.LogPHPRequest(r, rel, http.StatusForbidden, 0)
			return
		}
		// 目录访问：尝试 index 文件，否则 403 禁止目录列表
		fi, err := os.Stat(scriptPath)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			logger.LogPHPRequest(r, rel, http.StatusNotFound, 0)
			return
		}
		if fi.IsDir() {
			// 尝试 index 文件
			for _, idx := range cfg.Index {
				cand := filepath.Join(scriptPath, idx)
				if fileExists(cand) {
					scriptPath = cand
					rel = filepath.Join(rel, idx)
					fi, _ = os.Stat(scriptPath)
					goto found
				}
			}
			// 无 index 文件 → 禁止目录列表
			w.WriteHeader(http.StatusForbidden)
			logger.LogPHPRequest(r, rel, http.StatusForbidden, 0)
			return
		}
	found:
		src, err := readFile(scriptPath)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			logger.LogPHPRequest(r, rel, http.StatusNotFound, 0)
			return
		}

		// 静态文件（非 PHP 脚本）直接以文件流返回，不经 phpgo 解释，
		// 否则标签外内容会被 phpgo 丢弃导致空白页。
		// http.ServeFile 会根据扩展名自动设置 Content-Type，
		// 支持 jar/txt/js/json/xml/md/py/html/htm/jpg/jpeg/png/m3u 等。
		if !isPHPScript(scriptPath, src) {
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			http.ServeFile(rec, r, scriptPath)
			logger.LogPHPRequest(r, rel, rec.status, rec.bytesSent)
			return
		}

		env := phpgo.NewDefaultEnv(client)
		// 注入超全局
		r.ParseForm()
		for k, vs := range r.Form {
			env.SetGet(k, vs[0])
			env.SetPost(k, vs[0])
		}
		for _, c := range r.Cookies() {
			env.SetCookie(c.Name, c.Value)
		}
		env.SetRequestURI(r.URL.RequestURI())
		env.SetServer("REQUEST_METHOD", r.Method)
		env.SetServer("REMOTE_ADDR", r.RemoteAddr)
		env.SetServer("SCRIPT_NAME", r.URL.Path)
		env.SetServer("QUERY_STRING", r.URL.RawQuery)
		env.SetServer("REQUEST_URI", r.URL.RequestURI())
		// HTTP headers → $_SERVER（PHP 风格：HTTP_ 前缀 + 大写 + 下划线）
		for k, vs := range r.Header {
			// 跳过 Content-Type/Content-Length（PHP 单独处理）
			lk := strings.ToLower(k)
			if lk == "content-type" || lk == "content-length" {
				continue
			}
			// HTTP_HOST, HTTP_USER_AGENT, HTTP_X_FORWARDED_PROTO 等
			phpKey := "HTTP_" + strings.ToUpper(strings.ReplaceAll(k, "-", "_"))
			env.SetServer(phpKey, vs[0])
		}
		// SERVER_NAME 和 SERVER_PORT
		host := r.Host
		serverName := host
		serverPort := "80"
		if h, p, err := net.SplitHostPort(host); err == nil {
			serverName = h
			serverPort = p
		}
		if serverPort == "80" && r.TLS != nil {
			serverPort = "443"
		}
		env.SetServer("HTTP_HOST", host)
		env.SetServer("SERVER_NAME", serverName)
		env.SetServer("SERVER_PORT", serverPort)
		env.SetServer("SERVER_PROTOCOL", r.Proto)
		if r.TLS != nil {
			env.SetServer("HTTPS", "on")
		}
		body := ""
		if r.Body != nil {
			buf := make([]byte, 0, 4096)
			tmp := make([]byte, 1024)
			for {
				n, e := r.Body.Read(tmp)
				buf = append(buf, tmp[:n]...)
				if e != nil {
					break
				}
				if len(buf) > 1<<20 {
					break
				}
			}
			body = string(buf)
		}
		env.SetPHPInput(body)
		env.SetScriptPath(scriptPath)

		// 用 statusRecorder 包装 ResponseWriter 以捕获最终状态码
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		if err := phpgo.ServePHP(env, rec, src); err != nil {
			// 错误已在 ServePHP 写入响应
		logger.LogPHPRequest(r, rel, rec.status, rec.bytesSent)
		return
	}
	logger.LogPHPRequest(r, rel, rec.status, rec.bytesSent)
	}
}

// statusRecorder 包装 http.ResponseWriter，记录最终写入的状态码和响应大小。
type statusRecorder struct {
	http.ResponseWriter
	status   int
	wrote    bool
	bytesSent int64
}

func (sr *statusRecorder) WriteHeader(code int) {
	if !sr.wrote {
		sr.status = code
		sr.wrote = true
	}
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *statusRecorder) Write(p []byte) (int, error) {
	n, err := sr.ResponseWriter.Write(p)
	sr.bytesSent += int64(n)
	return n, err
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// isPHPScript 判断文件是否应由 phpgo 解释执行。
// 标准 PHP 扩展名（.php/.php3/.php4/.phtml/.inc）视为 PHP；
// 已知的二进制/静态文件扩展名（jar/ico/png/jpg/zip 等）永远不按 PHP 处理；
// 其它扩展名若内容含 PHP 开始标签（<?php / <?= / <?）也按 PHP 处理；
// 否则视为静态文件，直接以文件流返回。
func isPHPScript(path, src string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	// 标准 PHP 扩展名
	switch ext {
	case ".php", ".php3", ".php4", ".phtml", ".inc":
		return true
	}
	// 已知的二进制/静态文件扩展名：即使内容碰巧包含 PHP 标签字节也不当 PHP 处理
	switch ext {
	case ".jar", ".zip", ".apk", ".exe", ".dll", ".so", ".bin", ".dat",
		".png", ".jpg", ".jpeg", ".gif", ".bmp", ".webp", ".svg", ".ico",
		".mp3", ".mp4", ".avi", ".mkv", ".flv", ".wav", ".flac",
		".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx",
		".ttf", ".woff", ".woff2", ".otf", ".eot", ".wasm",
		".db", ".sqlite", ".key", ".pem", ".crt", ".cer", ".p12",
		".ts", ".m4s", ".mpd":
		return false
	}
	// 检查内容是否含 PHP 开始标签
	return strings.Contains(src, "<?php") || strings.Contains(src, "<?=") || strings.Contains(src, "<?")
}

func readFile(p string) (string, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
