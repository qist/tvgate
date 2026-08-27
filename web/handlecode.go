package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"html/template"

	"github.com/qist/tvgate/config"
)

// codeRoot 返回 PHP 脚本根目录（docroot），已为绝对路径
func (h *ConfigHandler) codeRoot() string {
	config.CfgMu.RLock()
	root := config.Cfg.PHP.DocRoot
	config.CfgMu.RUnlock()
	if root == "" {
		root = "/www"
	}
	return root
}

// safeJoin 将相对路径安全拼接进 root，严格防目录穿越（.. 逃逸 + 绝对路径逃逸）
func (h *ConfigHandler) safeJoin(root, rel string) (string, error) {
	// 1) 先规范化 rel 并去掉前导分隔符，使其成为纯相对路径（避免 Join 时第二个参数
	//    被当作绝对路径而丢弃 root，导致逃逸）
	rel = strings.TrimPrefix(filepath.Clean(rel), string(filepath.Separator))
	// 2) Clean 后若仍以 ".." 开头，说明意图逃逸到 root 上层，直接拒绝
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("非法路径：禁止穿越 docroot（%s）", rel)
	}
	abs := filepath.Join(root, rel)
	// 3) 必须落在 root 内（含 root 自身）
	if abs == root {
		return abs, nil
	}
	if !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return "", fmt.Errorf("非法路径：禁止穿越 docroot（%s）", abs)
	}
	return abs, nil
}

// assertInside 对路径做完整符号链接解析，确保真实文件仍落在 root 内
// （防止 docroot 内某层 symlink 指向外部目录而越权读写内容）
func (h *ConfigHandler) assertInside(root, abs string) error {
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if os.IsNotExist(err) {
			// 文件尚不存在（如尚未创建），无法解析，交由调用方处理
			return nil
		}
		return fmt.Errorf("无法解析路径：%v", err)
	}
	if real != root && !strings.HasPrefix(real, root+string(filepath.Separator)) {
		return fmt.Errorf("非法路径：符号链接指向 docroot 之外（%s）", real)
	}
	return nil
}

// handleCodeEditor 渲染代码文件管理器页面
func (h *ConfigHandler) handleCodeEditor(w http.ResponseWriter, r *http.Request) {
	webPath := h.getWebPath()
	if r.URL.Path == webPath+"code" {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		content, err := templatesFS.ReadFile("templates/code.html")
		if err != nil {
			http.Error(w, "Failed to read template: "+err.Error(), http.StatusInternalServerError)
			return
		}
		tmpl, err := template.New("code").Parse(string(content))
		if err != nil {
			http.Error(w, "Failed to parse template: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.Execute(w, map[string]interface{}{"webPath": webPath}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		return
	}
	http.NotFound(w, r)
}

// handleCodeList 列出指定目录下的文件/子目录（非递归，仅当前层级）
// 查询参数 dir 为相对 docroot 的子目录路径（空表示根目录）
func (h *ConfigHandler) handleCodeList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	root := h.codeRoot()
	subDir := r.URL.Query().Get("dir")
	targetDir := root
	if subDir != "" {
		abs, err := h.safeJoin(root, subDir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := h.assertInside(root, abs); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		targetDir = abs
	}
	info, err := os.Stat(targetDir)
	if err != nil || !info.IsDir() {
		http.Error(w, "目录不存在", http.StatusNotFound)
		return
	}
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		http.Error(w, "读取目录失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	var items []map[string]interface{}
	for _, e := range entries {
		// 跳过隐藏文件和备份文件
		name := e.Name()
		if strings.HasPrefix(name, ".") || strings.Contains(name, ".bak.") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, map[string]interface{}{
			"name":  name,
			"isDir": e.IsDir(),
			"size":  fi.Size(),
		})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "success", "items": items})
}

// handleCodeRead 读取文件内容（文本，以 JSON 返回避免 Edge DevTools hex 预览）
func (h *ConfigHandler) handleCodeRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	root := h.codeRoot()
	abs, err := h.safeJoin(root, r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.assertInside(root, abs); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		http.Error(w, "读取失败: "+err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"content": string(data),
	})
}

// handleCodeSave 保存文件内容（POST body 为文本，?path= 指定相对路径）
func (h *ConfigHandler) handleCodeSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	root := h.codeRoot()
	rel := r.URL.Query().Get("path")
	if rel == "" {
		http.Error(w, "缺少 path 参数", http.StatusBadRequest)
		return
	}
	abs, err := h.safeJoin(root, rel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "读取请求体失败: "+err.Error(), http.StatusBadRequest)
		return
	}
	// 自动创建父目录
	if dir := filepath.Dir(abs); dir != root {
		_ = os.MkdirAll(dir, 0755)
	}
	// 备份原文件（存在时先校验符号链接，避免越权读取外部文件）
	if _, e := os.Stat(abs); e == nil {
		if err := h.assertInside(root, abs); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		_ = copyFile(abs, abs+".bak."+timestamp())
	}
	if err := os.WriteFile(abs, body, 0644); err != nil {
		http.Error(w, "写入失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write([]byte(`{"status":"success","message":"已保存"}`))
}

// handleCodeNew 新建文件或目录（?path= & ?type=file|dir）
func (h *ConfigHandler) handleCodeNew(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	root := h.codeRoot()
	rel := r.URL.Query().Get("path")
	typ := r.URL.Query().Get("type")
	if rel == "" {
		http.Error(w, "缺少 path 参数", http.StatusBadRequest)
		return
	}
	abs, err := h.safeJoin(root, rel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if typ == "dir" {
		if err := os.MkdirAll(abs, 0755); err != nil {
			http.Error(w, "创建目录失败: "+err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		if dir := filepath.Dir(abs); dir != root {
			_ = os.MkdirAll(dir, 0755)
		}
		f, err := os.OpenFile(abs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			http.Error(w, "创建文件失败: "+err.Error(), http.StatusInternalServerError)
			return
		}
		f.Close()
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write([]byte(`{"status":"success","message":"已创建"}`))
}

// handleCodeDelete 删除文件或目录
func (h *ConfigHandler) handleCodeDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	root := h.codeRoot()
	rel := r.URL.Query().Get("path")
	if rel == "" {
		http.Error(w, "缺少 path 参数", http.StatusBadRequest)
		return
	}
	abs, err := h.safeJoin(root, rel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// 禁止删除 docroot 根目录本身
	if abs == root {
		http.Error(w, "禁止删除 docroot 根目录", http.StatusForbidden)
		return
	}
	if err := os.RemoveAll(abs); err != nil {
		http.Error(w, "删除失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write([]byte(`{"status":"success","message":"已删除"}`))
}

// handleCodeUpload 上传文件（multipart，字段 file + 可选 dir 前缀）
func (h *ConfigHandler) handleCodeUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	root := h.codeRoot()
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "解析上传失败: "+err.Error(), http.StatusBadRequest)
		return
	}
	dir := r.FormValue("dir")
	absDir, err := h.safeJoin(root, dir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	files := r.MultipartForm.File["file"]
	for _, fh := range files {
		name := filepath.Base(fh.Filename) // 防穿越：仅取文件名
		dst := filepath.Join(absDir, name)
		if absDir != root {
			_ = os.MkdirAll(absDir, 0755)
		}
		src, e := fh.Open()
		if e != nil {
			http.Error(w, "打开上传文件失败: "+e.Error(), http.StatusInternalServerError)
			return
		}
		dstFile, e := os.Create(dst)
		if e != nil {
			src.Close()
			http.Error(w, "写入失败: "+e.Error(), http.StatusInternalServerError)
			return
		}
		_, e = io.Copy(dstFile, src)
		src.Close()
		dstFile.Close()
		if e != nil {
			http.Error(w, "保存失败: "+e.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write([]byte(`{"status":"success","message":"上传完成"}`))
}

// handleCodeDownload 下载文件
func (h *ConfigHandler) handleCodeDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	root := h.codeRoot()
	abs, err := h.safeJoin(root, r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.assertInside(root, abs); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		http.Error(w, "读取失败: "+err.Error(), http.StatusNotFound)
		return
	}
	name := filepath.Base(abs)
	w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"\"")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Write(data)
}

// handleCodeCheck 对提交的 PHP 源码做简单语法检测（文本级，无需 PHP 运行时）
func (h *ConfigHandler) handleCodeCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var src string
	if r.URL.Query().Get("path") != "" {
		root := h.codeRoot()
		abs, err := h.safeJoin(root, r.URL.Query().Get("path"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		b, e := os.ReadFile(abs)
		if e != nil {
			http.Error(w, "读取失败: "+e.Error(), http.StatusNotFound)
			return
		}
		src = string(b)
	} else {
		b, e := io.ReadAll(r.Body)
		if e != nil {
			http.Error(w, "读取请求体失败", http.StatusBadRequest)
			return
		}
		src = string(b)
	}
	issues := simplePHPCheck(src)
	ok := true
	for _, i := range issues {
		if i.Level == "error" {
			ok = false
			break
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"ok":     ok,
		"issues": issues,
	})
}

// phpIssue 简单语法问题
type phpIssue struct {
	Level   string `json:"level"` // error | warning
	Message string `json:"message"`
	Line    int    `json:"line"`
}

// simplePHPCheck 纯文本级简单语法检测（不依赖 PHP 二进制）
func simplePHPCheck(src string) []phpIssue {
	var issues []phpIssue

	// 1) 必须以 PHP 开始标签开头（忽略前导空白/BOM）
	trimmed := strings.TrimSpace(src)
	if !strings.HasPrefix(trimmed, "<?php") && !strings.HasPrefix(trimmed, "<?") {
		issues = append(issues, phpIssue{"warning", "未以 <?php 或 <? 开始标签开头", 1})
	}

	// 2) 括号 / 引号配对扫描（跳过字符串与注释）
	pairs := map[rune]rune{')': '(', '}': '{', ']': '['}
	stack := []rune{}
	lineOf := 1
	inSingle, inDouble, inLineCmt, inBlockCmt := false, false, false, false
	runes := []rune(src)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if c == '\n' {
			lineOf++
			inLineCmt = false
			continue
		}
		if inLineCmt {
			continue
		}
		if inBlockCmt {
			if c == '*' && i+1 < len(runes) && runes[i+1] == '/' {
				inBlockCmt = false
				i++
			}
			continue
		}
		if inSingle {
			if c == '\\' {
				i++
				continue
			}
			if c == '\'' {
				inSingle = false
			}
			continue
		}
		if inDouble {
			if c == '\\' {
				i++
				continue
			}
			if c == '"' {
				inDouble = false
			}
			continue
		}
		switch c {
		case '/':
			if i+1 < len(runes) && runes[i+1] == '/' {
				inLineCmt = true
				continue
			}
			if i+1 < len(runes) && runes[i+1] == '*' {
				inBlockCmt = true
				i++
				continue
			}
		case '\'':
			inSingle = true
			continue
		case '"':
			inDouble = true
			continue
		case '(', '{', '[':
			stack = append(stack, c)
		case ')', '}', ']':
			if len(stack) == 0 {
				issues = append(issues, phpIssue{"error", fmt.Sprintf("多余的闭合符号 '%c'", c), lineOf})
				continue
			}
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if pairs[c] != top {
				issues = append(issues, phpIssue{"error", fmt.Sprintf("括号不匹配：'%c' 与 '%c'", top, c), lineOf})
			}
		}
	}
	for _, s := range stack {
		issues = append(issues, phpIssue{"error", fmt.Sprintf("未闭合的符号 '%c'", s), lineOf})
	}
	if inSingle {
		issues = append(issues, phpIssue{"error", "单引号字符串未闭合", lineOf})
	}
	if inDouble {
		issues = append(issues, phpIssue{"error", "双引号字符串未闭合", lineOf})
	}
	if inBlockCmt {
		issues = append(issues, phpIssue{"error", "块注释 /* 未闭合", lineOf})
	}

	return issues
}

// timestamp 生成备份时间戳
func timestamp() string {
	return strings.ReplaceAll(strings.ReplaceAll(time.Now().Format("2006-01-02 15:04:05"), " ", "_"), ":", "-")
}
