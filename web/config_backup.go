package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/qist/tvgate/config/load"
	"github.com/qist/tvgate/logger"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/qist/tvgate/config"
)

// ConfigBackupHandler 处理配置备份管理
type ConfigBackupHandler struct{}

// backupConfigFile 各配置编辑接口保存前的统一备份入口（带内容比对）。
// 返回生成的备份路径（跳过时为空串），err 非 nil 表示备份失败。
// 规则（只与磁盘当前配置和最近一份备份对比，避免每次保存全量扫描成百上千份历史备份）：
//  1. 新内容与当前磁盘配置一致（点了保存但没实际改动）→ 不备份
//  2. 当前磁盘状态与最近一份备份内容一致（刚保存过同状态）→ 不重复归档
//
// 备份内容始终为「改动前」的当前配置，还原点语义与历史一致。
func backupConfigFile(configPath string, newContent []byte) (string, error) {
	cur, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("读取当前配置失败: %w", err)
	}
	if bytes.Equal(cur, newContent) {
		// 内容没变化：不产生备份
		return "", nil
	}
	if latest, lerr := latestConfigBackup(configPath); lerr == nil {
		if b, berr := os.ReadFile(latest); berr == nil && bytes.Equal(b, cur) {
			// 当前状态刚备份过，不重复归档
			return "", nil
		}
	}
	return writeConfigBackup(configPath, cur)
}

// listConfigBackups 返回配置文件的所有备份路径（按文件名时间戳倒序，最新在前）。
func listConfigBackups(configPath string) ([]string, error) {
	dir := filepath.Dir(configPath)
	files, err := filepath.Glob(filepath.Join(dir, filepath.Base(configPath)+".backup.*"))
	if err != nil {
		return nil, err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	return files, nil
}

// latestConfigBackup 返回配置目录下最新的备份文件路径。
func latestConfigBackup(configPath string) (string, error) {
	files, err := listConfigBackups(configPath)
	if err != nil || len(files) == 0 {
		return "", fmt.Errorf("无备份文件")
	}
	return files[0], nil
}

// writeConfigBackup 立即把 content 写入一份带时间戳的备份文件。
// 时间戳精确到毫秒；同一毫秒内再次写入时追加递增后缀，避免互相覆盖。
func writeConfigBackup(configPath string, content []byte) (string, error) {
	base := configPath + ".backup." + time.Now().Format("20060102150405.000")
	backupPath := base
	for i := 2; ; i++ {
		if _, err := os.Stat(backupPath); os.IsNotExist(err) {
			break
		}
		backupPath = fmt.Sprintf("%s.%d", base, i)
	}
	if err := os.WriteFile(backupPath, content, 0644); err != nil {
		return "", err
	}
	return backupPath, nil
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

	// 确保传入的文件名是相对于配置目录的，或者在配置目录下
	var absFile string
	if filepath.IsAbs(file) {
		absFile = file
	} else {
		// 如果是相对路径，将其视为配置目录下的文件
		absFile = filepath.Join(dir, file)
	}

	// 再次转换为绝对路径，确保安全检查的准确性
	absFile, _ = filepath.Abs(absFile)

	// 确保规范化路径在配置目录下，使用更安全的路径检查方法
	normalizedDir, _ := filepath.Abs(dir)

	// 使用 strings.HasPrefix 并确保路径边界安全
	relPath, err := filepath.Rel(normalizedDir, absFile)
	if err != nil || strings.HasPrefix(relPath, "..") {
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

	// 确保传入的文件名是相对于配置目录的，或者在配置目录下
	var absFile string
	if filepath.IsAbs(file) {
		absFile = file
	} else {
		// 如果是相对路径，将其视为配置目录下的文件
		absFile = filepath.Join(dir, file)
	}

	// 再次转换为绝对路径，确保安全检查的准确性
	absFile, _ = filepath.Abs(absFile)

	// 确保规范化路径在配置目录下，使用更安全的路径检查方法
	normalizedDir, _ := filepath.Abs(dir)

	// 使用 strings.HasPrefix 并确保路径边界安全
	relPath, err := filepath.Rel(normalizedDir, absFile)
	if err != nil || strings.HasPrefix(relPath, "..") {
		http.Error(w, "不允许还原目录外的文件", http.StatusForbidden)
		return
	}

	data, err := os.ReadFile(absFile)
	if err != nil {
		http.Error(w, "读取备份失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 先备份当前配置（内容比对：与待还原内容一致或已有同态快照时跳过）
	if _, err := backupConfigFile(configPath, data); err != nil {
		http.Error(w, "创建备份失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 写入备份内容到当前配置
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		http.Error(w, "还原失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 立即重载内存配置：各 GET API 读 config.Cfg，磁盘变更要等 fsnotify
	// 防抖（reload: 5 秒）后才重新加载——保存后不等防抖，前端立即读到新值
	if err := load.LoadConfig(configPath); err != nil {
		logger.LogPrintf("保存后重载配置失败: %v", err)
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

	// 确保传入的文件名是相对于配置目录的，或者在配置目录下
	var absFile string
	if filepath.IsAbs(file) {
		absFile = file
	} else {
		// 如果是相对路径，将其视为配置目录下的文件
		absFile = filepath.Join(dir, file)
	}

	// 再次转换为绝对路径，确保安全检查的准确性
	absFile, _ = filepath.Abs(absFile)

	// 确保规范化路径在配置目录下，使用更安全的路径检查方法
	normalizedDir, _ := filepath.Abs(dir)

	// 使用 strings.HasPrefix 并确保路径边界安全
	relPath, err := filepath.Rel(normalizedDir, absFile)
	if err != nil || strings.HasPrefix(relPath, "..") {
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
	encodedFiles := r.Form["files"]

	if len(encodedFiles) == 0 {
		http.Error(w, "未提供要删除的文件", http.StatusBadRequest)
		return
	}

	// 解码文件路径
	files := make([]string, 0, len(encodedFiles))
	for _, encodedFile := range encodedFiles {
		decodedFile, err := url.QueryUnescape(encodedFile)
		if err != nil {
			// 如果解码失败，记录错误但继续处理其他文件
			continue
		}
		files = append(files, decodedFile)
	}

	if len(files) == 0 {
		http.Error(w, "所有文件路径解码失败", http.StatusBadRequest)
		return
	}

	configPath := *config.ConfigFilePath
	dir := filepath.Dir(configPath)

	// 批量删除文件
	successCount := 0
	errorCount := 0

	for _, file := range files {
		var absFile string

		// 如果是相对路径（不含分隔符），则认为是在配置目录下
		if filepath.IsAbs(file) {
			absFile = file
		} else {
			// 检查是否包含路径分隔符，如果没有，说明是单纯的文件名
			if !strings.Contains(file, string(os.PathSeparator)) {
				// 单纯的文件名，拼接配置目录路径
				absFile = filepath.Join(dir, file)
			} else {
				// 包含路径分隔符，认为是相对配置目录的路径
				absFile = filepath.Join(dir, file)
			}
		}

		// 再次转换为绝对路径，确保安全检查的准确性
		absFile, _ = filepath.Abs(absFile)

		// 确保规范化路径在配置目录下，使用更安全的路径检查方法
		normalizedDir, _ := filepath.Abs(dir)

		// 使用 strings.HasPrefix 并确保路径边界安全
		relPath, err := filepath.Rel(normalizedDir, absFile)
		if err != nil || strings.HasPrefix(relPath, "..") {
			errorCount++
			continue
		}

		if err := os.Remove(absFile); err != nil {
			errorCount++
		} else {
			successCount++
		}
	}

	respMessage := fmt.Sprintf("成功删除 %d 个备份，失败 %d 个", successCount, errorCount)
	if errorCount == 0 {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(fmt.Sprintf("成功删除 %d 个备份", successCount)))
	} else if successCount == 0 {
		http.Error(w, respMessage, http.StatusInternalServerError)
	} else {
		// 部分成功删除
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(respMessage))
	}
}

// handleCreateManualBackup 手动备份当前配置（配置备份管理页「手动备份」按钮）。
// 当前内容与最近一份备份一致时不重复归档。
func (h *ConfigBackupHandler) handleCreateManualBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "只支持 POST 请求", http.StatusMethodNotAllowed)
		return
	}
	configPath := *config.ConfigFilePath
	cur, err := os.ReadFile(configPath)
	if err != nil {
		http.Error(w, "读取当前配置失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if latest, lerr := latestConfigBackup(configPath); lerr == nil {
		if b, berr := os.ReadFile(latest); berr == nil && bytes.Equal(b, cur) {
			writeJSONResponse(w, http.StatusOK, map[string]interface{}{
				"status":  "success",
				"message": "当前配置与最近备份一致，未重复备份",
				"created": false,
			})
			return
		}
	}
	backupPath, err := writeConfigBackup(configPath, cur)
	if err != nil {
		http.Error(w, "创建备份失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"status":  "success",
		"message": "已备份当前配置",
		"created": true,
		"file":    filepath.Base(backupPath),
	})
}

// handleCleanupBackups 清理备份：按原始配置文件分组，每份保留最新 keep 个。
func (h *ConfigBackupHandler) handleCleanupBackups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "只支持 POST 请求", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Keep int `json:"keep"` // 每个文件保留几份备份（0=全删）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "解析请求失败: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Keep < 0 {
		http.Error(w, "keep 不能为负数", http.StatusBadRequest)
		return
	}

	configPath := *config.ConfigFilePath
	dir := filepath.Dir(configPath)
	files, err := filepath.Glob(filepath.Join(dir, "*.backup.*"))
	if err != nil {
		http.Error(w, "扫描备份失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// 按原始文件名分组
	groups := make(map[string][]string)
	for _, p := range files {
		base := filepath.Base(p)
		idx := strings.Index(base, ".backup.")
		if idx < 0 {
			continue
		}
		groups[base[:idx]] = append(groups[base[:idx]], p)
	}
	deleted := 0
	for _, group := range groups {
		// 时间戳串在文件名中按字典序可排序：倒序 = 最新在前
		sort.Sort(sort.Reverse(sort.StringSlice(group)))
		for i := req.Keep; i < len(group); i++ {
			if os.Remove(group[i]) == nil {
				deleted++
			}
		}
	}
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"status":  "success",
		"message": fmt.Sprintf("已清理 %d 个备份，每组保留 %d 份", deleted, req.Keep),
		"deleted": deleted,
		"keep":    req.Keep,
	})
}

// writeJSONResponse 输出 JSON 响应
func writeJSONResponse(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(payload)
}
