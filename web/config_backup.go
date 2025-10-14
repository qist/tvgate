package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"
	"net/url"
	"sort"

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
	dir, err := filepath.Abs(filepath.Dir(configPath))
	if err != nil {
		http.Error(w, "获取配置目录失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 防止绝对路径，始终在配置目录下查找
	target := filepath.Join(dir, file)
	absTarget, err := filepath.Abs(target)
	if err != nil {
		http.Error(w, "解析目标文件路径失败: "+err.Error(), http.StatusBadRequest)
		return
	}

	// 确保文件在配置目录下
	if len(absTarget) <= len(dir) || absTarget[:len(dir)] != dir || (len(absTarget) > len(dir) && absTarget[len(dir)] != filepath.Separator) {
		http.Error(w, "不允许删除目录外的文件", http.StatusForbidden)
		return
	}

	if err := os.Remove(absTarget); err != nil {
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
	dir, err := filepath.Abs(filepath.Dir(configPath))
	if err != nil {
		http.Error(w, "获取配置目录失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	target := filepath.Join(dir, file)
	absTarget, err := filepath.Abs(target)
	if err != nil {
		http.Error(w, "解析目标文件路径失败: "+err.Error(), http.StatusBadRequest)
		return
	}
	// 确保文件在配置目录下
	if len(absTarget) <= len(dir) || absTarget[:len(dir)] != dir || (len(absTarget) > len(dir) && absTarget[len(dir)] != filepath.Separator) {
		http.Error(w, "不允许还原目录外的文件", http.StatusForbidden)
		return
	}

	data, err := os.ReadFile(absTarget)
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