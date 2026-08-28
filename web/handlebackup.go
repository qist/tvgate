package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// handleBackupPage 渲染备份文件中心页面
func (h *ConfigHandler) handleBackupPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	webPath := h.getWebPath()
	content, err := templatesFS.ReadFile("templates/backup.html")
	if err != nil {
		http.Error(w, "Failed to read template: "+err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl, err := template.New("backup").Parse(string(content))
	if err != nil {
		http.Error(w, "Failed to parse template: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl.Execute(w, map[string]interface{}{"webPath": webPath})
}

// backupItem 备份文件信息
type backupItem struct {
	Name     string `json:"name"`     // 备份文件名（含 .bak.时间戳）
	Original string `json:"original"` // 原始文件名
	Time     string `json:"time"`     // 备份时间（可读格式）
	Size     int64  `json:"size"`    // 文件大小
}

// handleBackupList 列出所有备份文件
func (h *ConfigHandler) handleBackupList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	root := h.codeRoot()
	dir := r.URL.Query().Get("dir")
	targetDir := root
	if dir != "" {
		abs, err := h.resolvePath(root, dir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		targetDir = abs
	}

	// 递归扫描所有 .bak. 文件
	var items []backupItem
	filepath.Walk(targetDir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		name := filepath.Base(p)
		if !strings.Contains(name, ".bak.") {
			return nil
		}
		// 解析原始文件名和时间戳
		// 格式: originalfile.bak.2026-08-27_22-28-00
		idx := strings.Index(name, ".bak.")
		original := name[:idx]
		tsStr := name[idx+5:] // 时间戳部分

		// 转换时间戳为可读格式
		t, _ := time.Parse("2006-01-02_15-04-05", tsStr)
		timeStr := tsStr
		if !t.IsZero() {
			timeStr = t.Format("2006-01-02 15:04:05")
		}

		// 相对路径（转换为 UTF-8 显示）
		rel, _ := filepath.Rel(root, p)
		relUTF8 := filepath.ToSlash(decodeFilename(rel))
		origUTF8 := decodeFilename(original)
		items = append(items, backupItem{
			Name:     relUTF8,
			Original: origUTF8,
			Time:     timeStr,
			Size:     info.Size(),
		})
		return nil
	})

	// 按时间倒序
	sort.Slice(items, func(i, j int) bool {
		return items[i].Time > items[j].Time
	})

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"items":  items,
	})
}

// handleBackupRestore 回滚：用备份文件覆盖当前文件
func (h *ConfigHandler) handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	root := h.codeRoot()
	bakPath := r.URL.Query().Get("path")
	if bakPath == "" {
		http.Error(w, "缺少 path 参数", http.StatusBadRequest)
		return
	}
	bakAbs, err := h.resolvePath(root, bakPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.assertInside(root, bakAbs); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	// 从备份文件名解析出原始文件路径
	bakName := filepath.Base(bakAbs)
	idx := strings.Index(bakName, ".bak.")
	if idx < 0 {
		http.Error(w, "不是有效的备份文件", http.StatusBadRequest)
		return
	}
	originalName := bakName[:idx]
	originalAbs := filepath.Join(filepath.Dir(bakAbs), originalName)

	// 先备份当前文件（如果存在）
	if _, e := os.Stat(originalAbs); e == nil {
		_ = copyFile(originalAbs, originalAbs+".bak."+timestamp())
	}

	// 复制备份文件到原始文件
	if err := copyFile(bakAbs, originalAbs); err != nil {
		http.Error(w, "回滚失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "已回滚：" + decodeFilename(originalName),
	})
}

// handleBackupDelete 删除单个备份文件
func (h *ConfigHandler) handleBackupDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	root := h.codeRoot()
	bakPath := r.URL.Query().Get("path")
	if bakPath == "" {
		http.Error(w, "缺少 path 参数", http.StatusBadRequest)
		return
	}
	bakAbs, err := h.resolvePath(root, bakPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.assertInside(root, bakAbs); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if !strings.Contains(filepath.Base(bakAbs), ".bak.") {
		http.Error(w, "只能删除备份文件", http.StatusBadRequest)
		return
	}
	if err := os.Remove(bakAbs); err != nil {
		http.Error(w, "删除失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "已删除备份",
	})
}

// handleBackupBatchDelete 批量删除备份文件
func (h *ConfigHandler) handleBackupBatchDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	root := h.codeRoot()
	var req struct {
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "解析请求失败: "+err.Error(), http.StatusBadRequest)
		return
	}
	deleted := 0
	failed := 0
	for _, p := range req.Paths {
		bakAbs, err := h.resolvePath(root, p)
		if err != nil {
			failed++
			continue
		}
		if err := h.assertInside(root, bakAbs); err != nil {
			failed++
			continue
		}
		if !strings.Contains(filepath.Base(bakAbs), ".bak.") {
			failed++
			continue
		}
		if err := os.Remove(bakAbs); err != nil {
			failed++
			continue
		}
		deleted++
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": fmt.Sprintf("已删除 %d 个备份，失败 %d 个", deleted, failed),
		"deleted": deleted,
		"failed":  failed,
	})
}

// handleBackupDownload 下载备份文件
func (h *ConfigHandler) handleBackupDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	root := h.codeRoot()
	bakPath := r.URL.Query().Get("path")
	if bakPath == "" {
		http.Error(w, "缺少 path 参数", http.StatusBadRequest)
		return
	}
	bakAbs, err := h.resolvePath(root, bakPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.assertInside(root, bakAbs); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	data, err := os.ReadFile(bakAbs)
	if err != nil {
		http.Error(w, "读取失败: "+err.Error(), http.StatusNotFound)
		return
	}
	name := decodeFilename(filepath.Base(bakAbs))
	w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"\"")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Write(data)
}

// handleBackupCleanup 清理指定文件的所有备份（保留最新N个）
func (h *ConfigHandler) handleBackupCleanup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	root := h.codeRoot()
	var req struct {
		Path    string `json:"path"`     // 原始文件相对路径（空=全部）
		Keep    int    `json:"keep"`     // 每个文件保留几个备份（0=全删）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "解析请求失败: "+err.Error(), http.StatusBadRequest)
		return
	}

	// 收集所有备份文件
	var allBackups []string
	scanDir := root
	var origOnDisk string // 磁盘上的原始文件名（可能是 GBK）
	if req.Path != "" {
		abs, err := h.resolvePath(root, req.Path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		scanDir = filepath.Dir(abs)
		origOnDisk = filepath.Base(abs)
	}

	filepath.Walk(scanDir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.Contains(filepath.Base(p), ".bak.") {
			return nil
		}
		// 如果指定了文件，只处理该文件的备份
		if req.Path != "" {
			origUTF8 := filepath.Base(req.Path)
			bp := filepath.Base(p)
			if !strings.HasPrefix(bp, origOnDisk+".bak.") &&
				!strings.HasPrefix(bp, origUTF8+".bak.") {
				return nil
			}
		}
		allBackups = append(allBackups, p)
		return nil
	})

	// 按文件分组，每组按时间倒序，保留前 Keep 个
	groups := make(map[string][]string)
	for _, p := range allBackups {
		base := filepath.Base(p)
		idx := strings.Index(base, ".bak.")
		orig := base[:idx]
		dir := filepath.Dir(p)
		key := filepath.Join(dir, orig)
		groups[key] = append(groups[key], p)
	}
	// 每组内排序（文件名倒序 = 时间倒序）
	deleted := 0
	for _, group := range groups {
		sort.Sort(sort.Reverse(sort.StringSlice(group)))
		for i := req.Keep; i < len(group); i++ {
			os.Remove(group[i])
			deleted++
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": fmt.Sprintf("已清理 %d 个备份文件", deleted),
		"deleted": deleted,
	})
}
