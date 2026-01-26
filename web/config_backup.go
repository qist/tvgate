package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/qist/tvgate/config"
)

// ConfigBackupHandler 处理配置备份管理
type ConfigBackupHandler struct{}

// handleConfigBackupPage 渲染备份管理页面
func (h *ConfigHandler) handleConfigBackupPage(w http.ResponseWriter, r *http.Request) {
	webPath := h.getWebPath()
	data := map[string]interface{}{
		"title":   "配置备份管理",
		"webPath": webPath,
	}
	h.renderTemplate(w, r, "config_backup", "templates/config_backup.html", data)
}

// handleListBackups 返回 JSON 备份列表，按时间从新到旧排序
func (h *ConfigBackupHandler) handleListBackups(w http.ResponseWriter, r *http.Request) {
	configPath := *config.ConfigFilePath
	dir := filepath.Dir(configPath)

	files, err := filepath.Glob(filepath.Join(dir, "*.backup.*"))
	if err != nil {
		http.Error(w, "获取备份列表失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 按修改时间排序，从新到旧
	sort.Slice(files, func(i, j int) bool {
		fileInfoI, errI := os.Stat(files[i])
		fileInfoJ, errJ := os.Stat(files[j])

		// 如果获取文件信息失败，则将该文件排在后面
		if errI != nil {
			return false
		}
		if errJ != nil {
			return true
		}

		// 按修改时间从新到旧排序
		return fileInfoI.ModTime().After(fileInfoJ.ModTime())
	})

	resp := map[string]interface{}{
		"backups": files,
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(resp)
}

// handleDeleteBackup 删除指定备份
func (h *ConfigBackupHandler) handleDeleteBackup(w http.ResponseWriter, r *http.Request) {
	file := r.URL.Query().Get("file")
	if file == "" {
		http.Error(w, "参数 file 必须提供", http.StatusBadRequest)
		return
	}

	configPath := *config.ConfigFilePath
	dir := filepath.Dir(configPath)
	absFile, _ := filepath.Abs(file)

	// 确保文件在配置目录下
	if !filepath.HasPrefix(absFile, dir) {
		http.Error(w, "不允许删除目录外的文件", http.StatusForbidden)
		return
	}

	if err := os.Remove(absFile); err != nil {
		http.Error(w, "删除备份失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("删除成功"))
}

// handleRestoreBackup 将指定备份还原为当前配置
func (h *ConfigBackupHandler) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	file := r.URL.Query().Get("file")
	if file == "" {
		http.Error(w, "参数 file 必须提供", http.StatusBadRequest)
		return
	}

	configPath := *config.ConfigFilePath
	dir := filepath.Dir(configPath)
	absFile, _ := filepath.Abs(file)

	if !filepath.HasPrefix(absFile, dir) {
		http.Error(w, "不允许还原目录外的文件", http.StatusForbidden)
		return
	}

	data, err := os.ReadFile(absFile)
	if err != nil {
		http.Error(w, "读取备份失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 先备份当前配置
	origData, _ := os.ReadFile(configPath)
	backupPath := configPath + ".backup." + time.Now().Format("20060102150405")
	os.WriteFile(backupPath, origData, 0644)

	// 写入备份内容到当前配置
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		http.Error(w, "还原失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("还原成功"))
}

// handleDownloadBackup 提供备份文件下载
func (h *ConfigBackupHandler) handleDownloadBackup(w http.ResponseWriter, r *http.Request) {
	file := r.URL.Query().Get("file")
	if file == "" {
		http.Error(w, "参数 file 必须提供", http.StatusBadRequest)
		return
	}

	configPath := *config.ConfigFilePath
	dir := filepath.Dir(configPath)
	absFile, _ := filepath.Abs(file)

	// 确保文件在配置目录下
	if !filepath.HasPrefix(absFile, dir) {
		http.Error(w, "不允许下载目录外的文件", http.StatusForbidden)
		return
	}

	// 检查文件是否存在
	if _, err := os.Stat(absFile); os.IsNotExist(err) {
		http.Error(w, "文件不存在", http.StatusNotFound)
		return
	}

	// 设置响应头以便下载
	filename := filepath.Base(absFile)
	// 对文件名进行URL编码以处理特殊字符
	encodedFilename := url.QueryEscape(filename)
	w.Header().Set("Content-Disposition", "attachment; filename="+encodedFilename)
	w.Header().Set("Content-Type", "application/octet-stream")

	// 读取文件并写入响应
	http.ServeFile(w, r, absFile)
}

// handleBatchDeleteBackups 批量删除备份
func (h *ConfigBackupHandler) handleBatchDeleteBackups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "只支持 POST 请求", http.StatusMethodNotAllowed)
		return
	}

	// 解析表单数据
	r.ParseMultipartForm(10 << 20) // 10MB 限制
	files := r.Form["files"]

	if len(files) == 0 {
		http.Error(w, "未提供要删除的文件", http.StatusBadRequest)
		return
	}

	configPath := *config.ConfigFilePath
	dir := filepath.Dir(configPath)

	// 批量删除文件
	successCount := 0
	errorCount := 0

	for _, file := range files {
		absFile, _ := filepath.Abs(file)

		// 确保文件在配置目录下
		if !filepath.HasPrefix(absFile, dir) {
			errorCount++
			continue
		}

		if err := os.Remove(absFile); err != nil {
			errorCount++
		} else {
			successCount++
		}
	}

	if errorCount > 0 {
		http.Error(w, fmt.Sprintf("成功删除 %d 个备份，失败 %d 个", successCount, errorCount), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(fmt.Sprintf("成功删除 %d 个备份", successCount)))
}
