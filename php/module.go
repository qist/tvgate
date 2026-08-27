package php

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/phpgo"
	utilshttp "github.com/qist/tvgate/utils/http"
)

var (
	cfg     *config.PHPConfig
	client  *http.Client
	docRoot string
)

// Init 初始化纯 Go PHP 模块。
// 构建统一单二进制：从磁盘读取 cfg.PHP.DocRoot（可配置，默认 /www）脚本，
// 由 phpgo 解释器执行。复用 TVGate 的 HTTP client（含 DNS/代理能力）。
func Init(c *config.Config) error {
	cfg = &c.PHP
	client = utilshttp.NewHTTPClient(c, nil)
	docRoot = cfg.DocRoot
	if docRoot == "" {
		docRoot = "/www"
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
		// 解析脚本路径（防目录穿越）。先剥掉路由前缀（如 /php/）
		p := r.URL.Path
		if cfg.Path != "" {
			p = strings.TrimPrefix(p, cfg.Path)
		}
		rel := strings.Trim(p, "/")
		if rel == "" {
			rel = "index.php"
		}
		// 默认索引
		if filepath.Ext(rel) == "" {
			for _, idx := range cfg.Index {
				cand := filepath.Join(docRoot, rel, idx)
				if fileExists(cand) {
					rel = filepath.Join(rel, idx)
					break
				}
			}
		}
		scriptPath := filepath.Join(docRoot, rel)
		// 防穿越
		if !strings.HasPrefix(scriptPath, docRoot) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		src, err := readFile(scriptPath)
		if err != nil {
			http.Error(w, "script not found: "+rel, http.StatusNotFound)
			return
		}

		// 静态文件（非 PHP 脚本）直接以文件流返回，不经 phpgo 解释，
		// 否则标签外内容会被 phpgo 丢弃导致空白页。
		if !isPHPScript(scriptPath, src) {
			http.ServeFile(w, r, scriptPath)
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

		if err := phpgo.ServePHP(env, w, src); err != nil {
			// 错误已在 ServePHP 写入响应
			return
		}
	}
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// isPHPScript 判断文件是否应由 phpgo 解释执行。
// 标准 PHP 扩展名（.php/.php3/.php4/.phtml/.inc）视为 PHP；
// 其它扩展名若内容含 PHP 开始标签（<?php / <?= / <?）也按 PHP 处理；
// 否则视为静态文件，直接以文件流返回。
func isPHPScript(path, src string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".php", ".php3", ".php4", ".phtml", ".inc":
		return true
	}
	return strings.Contains(src, "<?php") || strings.Contains(src, "<?=") || strings.Contains(src, "<?")
}

func readFile(p string) (string, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
