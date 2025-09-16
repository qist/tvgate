package web

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed config.yaml
var embeddedConfig embed.FS

// EnsureConfigFile 确保指定路径下有配置文件
// - configPath 可以是目录或文件路径
// - 如果文件存在则直接返回，不会覆盖
// - 如果目录不存在会自动创建
func EnsureConfigFile(configPath string) (string, error) {
	if configPath == "" {
		configPath = "config.yaml"
	}

	var configFilePath string

	info, err := os.Stat(configPath)
	if err == nil && info.IsDir() {
		// 是目录 -> 拼接默认文件名
		configFilePath = filepath.Join(configPath, "config.yaml")
	} else if os.IsNotExist(err) {
		// 不存在 -> 判断是否可能是目录
		ext := filepath.Ext(configPath)
		if ext == "" {
			// 没有扩展名，可能是目录 -> 拼接默认文件名
			configFilePath = filepath.Join(configPath, "config.yaml")
		} else {
			// 有扩展名，视为文件路径
			configFilePath = configPath
		}
	} else {
		// 已存在但不是目录 -> 直接当文件路径
		configFilePath = configPath
	}

	configDir := filepath.Dir(configFilePath)

	// 文件已存在，直接返回
	if _, err := os.Stat(configFilePath); err == nil {
		return configFilePath, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("检查配置文件时出错: %w", err)
	}

	// 读取 embedded 默认配置
	content, err := embeddedConfig.ReadFile("config.yaml")
	if err != nil {
		return "", fmt.Errorf("读取嵌入配置文件失败: %w", err)
	}

	// 确保目录存在
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", fmt.Errorf("创建配置目录失败: %w", err)
	}

	// 写入新文件
	if err := os.WriteFile(configFilePath, content, 0644); err != nil {
		return "", fmt.Errorf("写入配置文件失败: %w", err)
	}

	return configFilePath, nil
}
