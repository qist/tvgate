package sync

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/web"
)

// ApplyResult 一次同步的统计结果
type ApplyResult struct {
	Updated   int // 覆盖已存在文件
	Added     int // 新增文件
	Deleted   int // 删除文件
	Errors    []string
}

// safeLocalPath 校验 relPath 安全并返回 localRoot 下的绝对路径（防 ../ 穿越）。
func safeLocalPath(localRoot, relPath string) (string, error) {
	rel := filepath.Clean(relPath)
	if rel == "." || rel == "" {
		return "", fmt.Errorf("非法相对路径: %q", relPath)
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("非法绝对路径: %q", relPath)
	}
	target := filepath.Join(localRoot, rel)
	if target != localRoot && !strings.HasPrefix(target, localRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("路径穿越被拒绝: %q", relPath)
	}
	return target, nil
}

// isProtected 判断 relPath 是否落在 protect 保护清单内（支持目录前缀）。
func isProtected(protect []string, relPath string) bool {
	rel := filepath.Clean(relPath)
	for _, p := range protect {
		p = filepath.Clean(p)
		if p == "." || p == "" {
			continue
		}
		if rel == p || strings.HasPrefix(rel, p+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// isPHPFile 判断是否为 PHP 脚本（覆盖前需语法校验）。
func isPHPFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".php", ".phtml", ".php3", ".php4", ".inc":
		return true
	}
	return false
}

func backupEnabled(cfg *config.SyncConfig) bool {
	return cfg != nil && cfg.Backup != nil && *cfg.Backup
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0644)
}

// ApplyUpdate 应用单个文件更新：语法校验 → 临时文件 → 备份 → 原子替换。
func ApplyUpdate(localRoot string, cfg *config.SyncConfig, relPath string, content []byte, result *ApplyResult) error {
	target, err := safeLocalPath(localRoot, relPath)
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		return err
	}
	// 语法校验：仅 PHP 脚本；有 error 级问题拒绝覆盖（其余文件继续）
	if isPHPFile(relPath) {
		for _, iss := range web.SimplePHPCheck(string(content)) {
			if iss.Level == "error" {
				e := fmt.Errorf("PHP 语法校验拒绝 %s: %s", relPath, iss.Message)
				logger.LogPrintf("⚠️ [sync] %v", e)
				result.Errors = append(result.Errors, e.Error())
				return e
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		result.Errors = append(result.Errors, err.Error())
		return err
	}
	existed := false
	if _, err := os.Stat(target); err == nil {
		existed = true
		if backupEnabled(cfg) {
			if err := copyFile(target, fmt.Sprintf("%s.bak.%s", target, time.Now().Format("20060102-150405"))); err != nil {
				logger.LogPrintf("⚠️ [sync] 备份失败 %s: %v", target, err)
			}
		}
	}
	// 原子替换：写临时文件再 rename，避免半写文件被 PHP 模块读到
	tmp := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+".sync.tmp")
	if err := os.WriteFile(tmp, content, 0644); err != nil {
		result.Errors = append(result.Errors, err.Error())
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		result.Errors = append(result.Errors, err.Error())
		return err
	}
	if existed {
		result.Updated++
	} else {
		result.Added++
	}
	return nil
}

// ApplyDelete 删除孤立文件（远端已删）：先备份再删除。
func ApplyDelete(localRoot string, cfg *config.SyncConfig, relPath string, result *ApplyResult) error {
	target, err := safeLocalPath(localRoot, relPath)
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		return err
	}
	if _, err := os.Stat(target); err != nil {
		return nil // 已不存在，视为成功
	}
	if backupEnabled(cfg) {
		_ = copyFile(target, fmt.Sprintf("%s.bak.%s", target, time.Now().Format("20060102-150405")))
	}
	if err := os.Remove(target); err != nil {
		result.Errors = append(result.Errors, err.Error())
		return err
	}
	result.Deleted++
	return nil
}
