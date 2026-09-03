package php

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/phpgo"
)

// captureRecorder 内部执行的输出捕获器：不写真实连接，缓存状态/头/体。
type captureRecorder struct {
	header http.Header
	buf    bytes.Buffer
	status int
	wrote  bool
}

func (c *captureRecorder) Header() http.Header {
	if c.header == nil {
		c.header = make(http.Header)
	}
	return c.header
}

func (c *captureRecorder) WriteHeader(code int) {
	if !c.wrote {
		c.status = code
		c.wrote = true
	}
}

func (c *captureRecorder) Write(p []byte) (int, error) {
	if !c.wrote {
		c.status = http.StatusOK
		c.wrote = true
	}
	return c.buf.Write(p)
}

// Flush 满足 http.Flusher（phpgo flush 时安全空操作）
func (c *captureRecorder) Flush() {}

// resolveScriptPath 解析 docroot 相对路径到实际脚本文件。
// 兼容「php://php/akmg.php」与「php://akmg.php」两种写法：优先按原样，
// 不存在且以 php/ 开头时回退去前缀（对应 URL 挂载段 /php/ 的直觉写法）。
// 返回空表示脚本不存在/路径越界。
func resolveScriptPath(rel string) string {
	rel = strings.Trim(rel, "/")
	if rel == "" {
		return ""
	}
	candidates := []string{rel}
	if rest := strings.TrimPrefix(rel, "php/"); rest != rel {
		candidates = append(candidates, rest)
	}
	for _, cand := range candidates {
		p := filepath.Join(docRoot, cand)
		// 防目录穿越：必须是 docRoot 本身或其子路径
		if p != docRoot && !strings.HasPrefix(p, docRoot+string(filepath.Separator)) {
			continue
		}
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() {
			continue
		}
		// 防符号链接逃逸：真实路径必须仍落在 docRoot 内
		real, err := filepath.EvalSymlinks(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			continue
		}
		if real != resolvedDocRoot && !strings.HasPrefix(real, resolvedDocRoot+string(filepath.Separator)) {
			continue
		}
		return p
	}
	return ""
}

// Capture 内部执行 docroot 相对路径的 PHP 脚本并捕获输出（状态码/响应头/响应体）。
// 供播放器 php:// 频道源等内部调用：不走 HTTP 回环、无 IP 依赖、不经过鉴权与监控登记。
// 内部请求语义为 GET（query 注入 $_GET/$_POST/$_REQUEST），无 Cookie。
func Capture(rel string, query url.Values) (int, http.Header, []byte, error) {
	if cfg == nil || docRoot == "" {
		return 0, nil, nil, errors.New("php 模块未初始化")
	}
	scriptPath := resolveScriptPath(rel)
	if scriptPath == "" {
		return 0, nil, nil, fmt.Errorf("php 脚本不存在或路径越界: %s", rel)
	}
	src, err := readFile(scriptPath)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("读取 php 脚本失败: %w", err)
	}

	// 构造内部 GET 请求
	u := &url.URL{Path: "/" + strings.Trim(rel, "/"), RawQuery: query.Encode()}
	req := &http.Request{
		Method:     http.MethodGet,
		URL:        u,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Host:       "127.0.0.1",
		RemoteAddr: "127.0.0.1:0",
		RequestURI: u.RequestURI(),
	}

	env := phpgo.NewDefaultEnv(client)
	// 注入超全局（与 Handler 对齐；内部请求无 Cookie）
	req.ParseForm()
	for k, vs := range req.Form {
		env.SetGet(k, vs[0])
		env.SetPost(k, vs[0])
	}
	env.SetRequestURI(req.URL.RequestURI())
	env.SetServer("REQUEST_METHOD", req.Method)
	env.SetServer("REMOTE_ADDR", "127.0.0.1")
	env.SetServer("SCRIPT_NAME", req.URL.Path)
	env.SetServer("SCRIPT_FILENAME", scriptPath)
	env.SetServer("PHP_SELF", req.URL.Path)
	env.SetServer("QUERY_STRING", req.URL.RawQuery)
	env.SetServer("REQUEST_URI", req.URL.RequestURI())
	env.SetServer("HTTP_HOST", req.Host)
	env.SetServer("SERVER_NAME", "127.0.0.1")
	env.SetServer("SERVER_PORT", "80")
	env.SetServer("SERVER_PROTOCOL", req.Proto)
	env.SetPHPInput("")
	env.SetScriptPath(scriptPath)

	rec := &captureRecorder{header: make(http.Header)}
	if err := phpgo.ServePHP(env, rec, src); err != nil {
		logger.LogPrintf("[php] 内部执行失败 rel=%s err=%v", rel, err)
		return rec.status, rec.header, rec.buf.Bytes(), err
	}
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	return rec.status, rec.header, rec.buf.Bytes(), nil
}
