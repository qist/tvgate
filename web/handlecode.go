package web

import (
	"archive/zip"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qist/tvgate/config"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// codeRoot 返回 PHP 脚本根目录（docroot）。
// 配置加载阶段已保证 DocRoot 非空且为绝对路径（相对路径会以配置文件目录为基准拼接），
// 故这里直接返回，不再设置旧的绝对 /www 兜底，避免与默认相对路径不一致。
func (h *ConfigHandler) codeRoot() string {
	config.CfgMu.RLock()
	root := config.Cfg.PHP.DocRoot
	config.CfgMu.RUnlock()
	return root
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
	// 归一化 root 的符号链接：Android 等系统上 docroot 路径本身可能含
	// 符号链接（如 /data/user/0 -> /data/data），EvalSymlinks(abs) 会把它
	// 解析成真实路径，若仍用未解析的 root 做前缀比较会误判为越权。
	resolvedRoot := root
	if rr, err := filepath.EvalSymlinks(root); err == nil {
		resolvedRoot = rr
	}
	if real != resolvedRoot && !strings.HasPrefix(real, resolvedRoot+string(filepath.Separator)) {
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
		abs, err := h.resolvePath(root, subDir)
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
		// 尝试将非 UTF-8 文件名（如 GBK）转换为 UTF-8
		displayName := decodeFilename(name)
		fi, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, map[string]interface{}{
			"name":  displayName,
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
	abs, err := h.resolvePath(root, r.URL.Query().Get("path"))
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
	abs, err := h.resolvePath(root, rel)
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
	abs, err := h.resolvePath(root, rel)
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

// handleCodeRename 重命名文件或目录（?path=旧相对路径 & newname=新文件名）
// 仅允许在 docroot 内重命名，且新名不能含路径分隔符（禁止穿越）。
func (h *ConfigHandler) handleCodeRename(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	root := h.codeRoot()
	rel := r.URL.Query().Get("path")
	newname := strings.TrimSpace(r.URL.Query().Get("newname"))
	if rel == "" || newname == "" {
		http.Error(w, "缺少 path / newname 参数", http.StatusBadRequest)
		return
	}
	// 新名必须是单一文件/目录名，禁止 . 、.. 、含路径分隔符（防穿越）
	if newname == "." || newname == ".." || strings.ContainsAny(newname, "/\\") {
		http.Error(w, "新名称不合法", http.StatusBadRequest)
		return
	}
	oldAbs, err := h.resolvePath(root, rel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.assertInside(root, oldAbs); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	// 新路径 = 原目录 + 新名称，且必须仍在 docroot 内
	newAbs := filepath.Join(filepath.Dir(oldAbs), newname)
	if newAbs != root && !strings.HasPrefix(newAbs, root+string(filepath.Separator)) {
		http.Error(w, "非法路径：禁止穿越 docroot", http.StatusForbidden)
		return
	}
	if _, e := os.Stat(newAbs); e == nil {
		http.Error(w, "同名文件或目录已存在", http.StatusConflict)
		return
	}
	if err := os.Rename(oldAbs, newAbs); err != nil {
		http.Error(w, "重命名失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write([]byte(`{"status":"success","message":"已重命名"}`))
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
	abs, err := h.resolvePath(root, rel)
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
// 上传完成后自动检测：如果同目录下存在 xxx.zip 和 xxx.zip.md5，
// 且 .md5 文件内容与 .zip 实际 MD5 一致，则自动解压 xxx.zip（覆盖模式）。
// 没有 .md5 文件不报错，就是普通上传。
func (h *ConfigHandler) handleCodeUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	root := h.codeRoot()
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		http.Error(w, "解析上传失败: "+err.Error(), http.StatusBadRequest)
		return
	}
	dir := r.FormValue("dir")
	absDir, err := h.resolvePath(root, dir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = os.MkdirAll(absDir, 0755)
	var uploaded []string
	files := r.MultipartForm.File["file"]
	for _, fh := range files {
		name := filepath.Base(fh.Filename)
		dst := filepath.Join(absDir, name)
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
		uploaded = append(uploaded, name)
	}

	// 上传完成后检测配套 .zip.md5，MD5 匹配则自动解压
	type autoUnzip struct {
		Zip    string `json:"zip"`
		MD5    string `json:"md5"`
		Status string `json:"status"`
		Files  int    `json:"files,omitempty"`
		Error  string `json:"error,omitempty"`
	}
	var autoResults []autoUnzip

	for _, name := range uploaded {
		if !strings.HasSuffix(strings.ToLower(name), ".zip") {
			continue
		}
		// 查找配套 .md5 文件：xxx.zip.md5
		md5Path := filepath.Join(absDir, name+".md5")
		md5Data, e := os.ReadFile(md5Path)
		if e != nil {
			continue // 没有 .md5 文件，正常跳过不报错
		}
		// 解析期望的 MD5 值（取第一段非空内容）
		expectedMD5 := strings.Fields(strings.TrimSpace(string(md5Data)))
		if len(expectedMD5) == 0 {
			continue
		}
		expected := strings.ToLower(expectedMD5[0])

		// 计算实际 zip 文件的 MD5
		zipPath := filepath.Join(absDir, name)
		actual, e := md5File(zipPath)
		if e != nil {
			autoResults = append(autoResults, autoUnzip{Zip: name, MD5: expected, Status: "error", Error: "计算 MD5 失败: " + e.Error()})
			continue
		}
		if actual != expected {
			autoResults = append(autoResults, autoUnzip{Zip: name, MD5: expected, Status: "mismatch", Error: "MD5 不匹配: 期望 " + expected + " 实际 " + actual})
			continue
		}
		// MD5 匹配，执行解压
		n, e := extractZip(zipPath, absDir, root, h)
		if e != nil {
			autoResults = append(autoResults, autoUnzip{Zip: name, MD5: expected, Status: "error", Error: "解压失败: " + e.Error()})
		} else {
			autoResults = append(autoResults, autoUnzip{Zip: name, MD5: expected, Status: "ok", Files: n})
		}
	}

	resp := map[string]interface{}{
		"status":  "success",
		"message": "上传完成",
	}
	if len(autoResults) > 0 {
		resp["unzip"] = autoResults
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(resp)
}

// handleCodeDownload 下载文件
func (h *ConfigHandler) handleCodeDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	root := h.codeRoot()
	abs, err := h.resolvePath(root, r.URL.Query().Get("path"))
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
	// 尝试将文件名转为 UTF-8 用于 Content-Disposition
	displayName := decodeFilename(name)
	w.Header().Set("Content-Disposition", "attachment; filename=\""+displayName+"\"; filename*=UTF-8''"+displayName)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Write(data)
}

// handleCodeUnzip 解压 ZIP 文件到指定目录（覆盖模式）。
// 支持两种模式：
//  1. 手动解压：POST ?path=xxx.zip&dir=目标目录（磁盘上已有 zip 文件）
//  2. 上传解压：POST multipart file=xxx.zip&dir=目标目录
//
// 可选 flatten=true 展平子目录。
func (h *ConfigHandler) handleCodeUnzip(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	root := h.codeRoot()

	// 模式1：通过 path 参数指定磁盘上已有的 zip 文件
	zipParam := r.URL.Query().Get("path")
	if zipParam != "" {
		zipAbs, err := h.resolvePath(root, zipParam)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := h.assertInside(root, zipAbs); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		// 目标目录：dir 参数或 zip 文件所在目录
		dir := r.URL.Query().Get("dir")
		var absDir string
		if dir != "" {
			absDir, err = h.resolvePath(root, dir)
		} else {
			absDir = filepath.Dir(zipAbs)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := h.assertInside(root, absDir); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		n, e := extractZip(zipAbs, absDir, root, h)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if e != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":  "error",
				"message": "解压失败: " + e.Error(),
			})
		} else {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":  "success",
				"files":   n,
				"errors":  0,
				"message": fmt.Sprintf("解压完成: %d 个文件", n),
			})
		}
		return
	}

	// 模式2：通过 multipart 上传 zip 文件
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		http.Error(w, "解析上传失败: "+err.Error(), http.StatusBadRequest)
		return
	}
	dir := r.FormValue("dir")
	flatten := r.FormValue("flatten") == "true"
	absDir, err := h.resolvePath(root, dir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.assertInside(root, absDir); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	_ = os.MkdirAll(absDir, 0755)
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		http.Error(w, "缺少 zip 文件", http.StatusBadRequest)
		return
	}
	type result struct {
		Name  string `json:"name"`
		Path  string `json:"path"`
		Size  int64  `json:"size"`
		Error string `json:"error,omitempty"`
	}
	var results []result
	totalFiles := 0
	totalErrors := 0
	for _, fh := range files {
		name := filepath.Base(fh.Filename)
		if !strings.HasSuffix(strings.ToLower(name), ".zip") {
			results = append(results, result{Name: name, Error: "非 zip 文件"})
			totalErrors++
			continue
		}
		src, e := fh.Open()
		if e != nil {
			results = append(results, result{Name: name, Error: "打开失败: " + e.Error()})
			totalErrors++
			continue
		}
		buf, e := io.ReadAll(io.LimitReader(src, 64<<20))
		src.Close()
		if e != nil {
			results = append(results, result{Name: name, Error: "读取失败: " + e.Error()})
			totalErrors++
			continue
		}
		zipReader, e := zip.NewReader(bytes.NewReader(buf), int64(len(buf)))
		if e != nil {
			results = append(results, result{Name: name, Error: "解析 zip 失败: " + e.Error()})
			totalErrors++
			continue
		}
		for _, zf := range zipReader.File {
			fname := strings.TrimPrefix(filepath.Clean(zf.Name), string(filepath.Separator))
			if fname == "" || fname == "." || strings.HasPrefix(fname, "..") {
				continue
			}
			if flatten {
				fname = filepath.Base(fname)
				if fname == "." || fname == "/" {
					continue
				}
			}
			dstPath := filepath.Join(absDir, fname)
			if !strings.HasPrefix(dstPath, absDir+string(filepath.Separator)) && dstPath != absDir {
				continue
			}
			if err := h.assertInside(root, dstPath); err != nil {
				continue
			}
			if zf.FileInfo().IsDir() {
				if !flatten {
					_ = os.MkdirAll(dstPath, 0755)
				}
				continue
			}
			if parent := filepath.Dir(dstPath); parent != absDir {
				_ = os.MkdirAll(parent, 0755)
			}
			out, e := os.Create(dstPath)
			if e != nil {
				results = append(results, result{Name: name, Path: fname, Error: "创建文件失败: " + e.Error()})
				totalErrors++
				continue
			}
			rc, e := zf.Open()
			if e != nil {
				out.Close()
				results = append(results, result{Name: name, Path: fname, Error: "打开 zip 条目失败: " + e.Error()})
				totalErrors++
				continue
			}
			_, e = io.Copy(out, rc)
			rc.Close()
			out.Close()
			if e != nil {
				results = append(results, result{Name: name, Path: fname, Error: "写入失败: " + e.Error()})
				totalErrors++
				continue
			}
			totalFiles++
		}
		results = append(results, result{Name: name, Path: dir, Size: int64(len(buf))})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"files":   totalFiles,
		"errors":  totalErrors,
		"results": results,
		"message": fmt.Sprintf("解压完成: %d 个文件, %d 个错误", totalFiles, totalErrors),
	})
}

func (h *ConfigHandler) handleCodeCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var src string
	if r.URL.Query().Get("path") != "" {
		root := h.codeRoot()
		abs, err := h.resolvePath(root, r.URL.Query().Get("path"))
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

// decodeFilename 尝试将文件名从 GBK 转为 UTF-8。
// 某些 GBK 字节序列恰好也是合法 UTF-8（但会被解码成希腊/亚美尼亚等字符，
// 而非中文），所以不能仅靠 utf8.ValidString 判断。
// 策略：如果有非 ASCII 字节，先尝试 GBK→UTF-8，如果结果含 CJK 字符就优先用；
// 如果 GBK 解码失败，再看原始是否合法 UTF-8。
func decodeFilename(name string) string {
	// 全 ASCII 直接返回
	isAllASCII := true
	for _, r := range name {
		if r > 127 {
			isAllASCII = false
			break
		}
	}
	if isAllASCII {
		return name
	}

	// 有非 ASCII 字节，先尝试 GBK 解码
	decoder := simplifiedchinese.GBK.NewDecoder()
	if gbkUTF8, err := decoder.String(name); err == nil && utf8.ValidString(gbkUTF8) {
		if containsCJK(gbkUTF8) {
			return gbkUTF8
		}
	}

	// GBK 失败或不含 CJK，尝试 GB18030
	gb18030Decoder := simplifiedchinese.GB18030.NewDecoder()
	if gb18030UTF8, err := gb18030Decoder.String(name); err == nil && utf8.ValidString(gb18030UTF8) {
		if containsCJK(gb18030UTF8) {
			return gb18030UTF8
		}
	}

	// 如果 GBK/GB18030 解码结果不含 CJK，但原始是合法 UTF-8，用原始
	if utf8.ValidString(name) {
		return name
	}

	// 最后兜底：返回 GBK 解码结果（即使不含 CJK）
	if gbkUTF8, err := decoder.String(name); err == nil {
		return gbkUTF8
	}
	if gb18030UTF8, err := gb18030Decoder.String(name); err == nil {
		return gb18030UTF8
	}

	return name
}

// containsCJK 检查字符串是否包含 CJK 统一汉字（U+4E00~U+9FFF）
func containsCJK(s string) bool {
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}

// encodeFilenameToGBK 尝试将 UTF-8 文件名转回 GBK 字节序列（磁盘上的原始字节）。
// 如果文件名是纯 ASCII 或转换失败，返回原文。
func encodeFilenameToGBK(name string) string {
	if utf8.ValidString(name) {
		// 如果全是 ASCII，不需要转换
		isASCII := true
		for _, r := range name {
			if r > 127 {
				isASCII = false
				break
			}
		}
		if isASCII {
			return name
		}
		// 尝试 UTF-8 → GBK
		encoder := simplifiedchinese.GBK.NewEncoder()
		gbkName, err := encoder.String(name)
		if err == nil {
			return gbkName
		}
		// GBK 失败，尝试 GB18030
		gb18030Encoder := simplifiedchinese.GB18030.NewEncoder()
		gbkName, err = gb18030Encoder.String(name)
		if err == nil {
			return gbkName
		}
	}
	return name
}

// resolvePath 将 UTF-8 相对路径解析为磁盘绝对路径。
// 先尝试直接拼接（UTF-8 文件名），如果文件不存在则尝试 GBK 转换。
// 这样前端始终用 UTF-8 文件名，后端自动处理编码匹配。
func (h *ConfigHandler) resolvePath(root, relPath string) (string, error) {
	if relPath == "" {
		return root, nil
	}
	// 安全检查
	rel := strings.TrimPrefix(filepath.Clean(relPath), string(filepath.Separator))
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("非法路径")
	}
	abs := filepath.Join(root, rel)
	// 检查是否在 root 内
	if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return "", fmt.Errorf("非法路径")
	}
	// 如果直接路径存在，返回
	if _, err := os.Stat(abs); err == nil {
		return abs, nil
	}
	// 文件不存在，尝试将每段路径从 UTF-8 转为 GBK 再拼接
	parts := strings.Split(rel, "/")
	var gbkParts []string
	for _, p := range parts {
		if p == "" {
			continue
		}
		gbkParts = append(gbkParts, encodeFilenameToGBK(p))
	}
	gbkAbs := filepath.Join(root, filepath.Join(gbkParts...))
	if _, err := os.Stat(gbkAbs); err == nil {
		return gbkAbs, nil
	}
	// 两种方式都不存在，返回原始路径（让后续报错）
	return abs, nil
}

// timestamp 生成备份时间戳
func timestamp() string {
	return strings.ReplaceAll(strings.ReplaceAll(time.Now().Format("2006-01-02 15:04:05"), " ", "_"), ":", "-")
}

// md5File 计算文件内容的 MD5 哈希值（返回小写十六进制字符串）
func md5File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractZip 将 zipPath 指向的 zip 文件解压到 destDir（覆盖模式）。
// root 用于防穿越校验。返回解压的文件数和错误。
func extractZip(zipPath, destDir, root string, h *ConfigHandler) (int, error) {
	data, err := os.ReadFile(zipPath)
	if err != nil {
		return 0, err
	}
	zipReader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return 0, err
	}
	count := 0
	for _, zf := range zipReader.File {
		fname := strings.TrimPrefix(filepath.Clean(zf.Name), string(filepath.Separator))
		if fname == "" || fname == "." || strings.HasPrefix(fname, "..") {
			continue
		}
		dstPath := filepath.Join(destDir, fname)
		// 防穿越：确保解压后路径仍在 destDir 内
		if !strings.HasPrefix(dstPath, destDir+string(filepath.Separator)) && dstPath != destDir {
			continue
		}
		// 防符号链接逃逸
		if err := h.assertInside(root, dstPath); err != nil {
			continue
		}
		if zf.FileInfo().IsDir() {
			_ = os.MkdirAll(dstPath, 0755)
			continue
		}
		// 确保父目录存在
		if parent := filepath.Dir(dstPath); parent != destDir {
			_ = os.MkdirAll(parent, 0755)
		}
		out, e := os.Create(dstPath)
		if e != nil {
			continue
		}
		rc, e := zf.Open()
		if e != nil {
			out.Close()
			continue
		}
		_, e = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if e != nil {
			continue
		}
		count++
	}
	return count, nil
}
