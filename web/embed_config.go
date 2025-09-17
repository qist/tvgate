package web

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed config.yaml
var embeddedConfig embed.FS

// EnsureConfigFile 确保指定路径下有配置文件
// 返回实际配置文件路径
func EnsureConfigFile(configPath string) (string, error) {
	if configPath == "" {
		configPath = "config.yaml"
	}

	var configFilePath string

	ext := filepath.Ext(configPath)

	if ext == "" || strings.HasSuffix(configPath, string(os.PathSeparator)) {
		// 没有扩展名或以 "/" 结尾 → 当目录处理
		configFilePath = filepath.Join(configPath, "config.yaml")
	} else {
		info, err := os.Stat(configPath)
		if err == nil {
			if info.IsDir() {
				// 已存在目录 → 当目录
				configFilePath = filepath.Join(configPath, "config.yaml")
			} else {
				// 已存在文件 → 当文件
				configFilePath = configPath
			}
		} else if os.IsNotExist(err) {
			// 不存在 → 默认当文件
			configFilePath = configPath
		} else {
			return "", fmt.Errorf("检查路径时出错: %w", err)
		}
	}

	configDir := filepath.Dir(configFilePath)

	// 文件已存在 → 不覆盖
	if _, err := os.Stat(configFilePath); err == nil {
		return configFilePath, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("检查配置文件时出错: %w", err)
	}

	// 读取嵌入配置
	content, err := embeddedConfig.ReadFile("config.yaml")
	if err != nil {
		return "", fmt.Errorf("读取嵌入配置文件失败: %w", err)
	}

	// 确保目录存在
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", fmt.Errorf("创建配置目录失败: %w", err)
	}

	// 写入文件
	if err := os.WriteFile(configFilePath, content, 0644); err != nil {
		return "", fmt.Errorf("写入配置文件失败: %w", err)
	}

	return configFilePath, nil
}
