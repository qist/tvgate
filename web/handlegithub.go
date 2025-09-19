package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/qist/tvgate/config"
	"gopkg.in/yaml.v3"
)

// handleGithubEditor 处理 GitHub 配置编辑器页面
func (h *ConfigHandler) handleGithubEditor(w http.ResponseWriter, r *http.Request) {
	webPath := h.getWebPath()

	data := map[string]interface{}{
		"title":   "TVGate GitHub 配置编辑器",
		"webPath": webPath,
	}

	if err := h.renderTemplate(w, r, "github_editor", "templates/github_editor.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleGithubConfig 处理 GitHub 配置获取请求
func (h *ConfigHandler) handleGithubConfig(w http.ResponseWriter, r *http.Request) {
	// 设置响应头
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	// 获取当前配置
	config.CfgMu.RLock()
	github := config.Cfg.Github
	config.CfgMu.RUnlock()

	// 转换为可JSON序列化的格式
	githubConfig := map[string]interface{}{
		"enabled":     github.Enabled,
		"url":         github.URL,
		"backup_urls": github.BackupURLs,
		"timeout":     formatDuration(github.Timeout),
		"retry":       github.Retry,
	}

	// 返回JSON格式的配置
	if err := json.NewEncoder(w).Encode(githubConfig); err != nil {
		http.Error(w, "序列化配置失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
}

// handleGithubConfigSave 处理 GitHub 配置保存请求
func (h *ConfigHandler) handleGithubConfigSave(w http.ResponseWriter, r *http.Request) {
	// 检查请求方法
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}

	// 读取请求体
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "读取请求体失败: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// 解析JSON数据
	var githubConfig map[string]interface{}
	if err := json.Unmarshal(body, &githubConfig); err != nil {
		http.Error(w, "解析JSON失败: "+err.Error(), http.StatusBadRequest)
		return
	}

	// 读取配置文件
	configPath := *config.ConfigFilePath
	data, err := os.ReadFile(configPath)
	if err != nil {
		http.Error(w, "读取配置文件失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 使用yaml.Node解析YAML配置以保持注释和格式
	var fullNode yaml.Node
	if err := yaml.Unmarshal(data, &fullNode); err != nil {
		http.Error(w, "解析配置文件失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 查找并更新github配置节点
	if err := updateGithubConfigNode(&fullNode, githubConfig); err != nil {
		http.Error(w, "更新配置失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 序列化更新后的配置
	updatedData, err := yaml.Marshal(&fullNode)
	if err != nil {
		http.Error(w, "序列化配置失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 写入配置文件
	if err := os.WriteFile(configPath, updatedData, 0644); err != nil {
		http.Error(w, "写入配置文件失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 更新内存中的配置
	config.CfgMu.Lock()
	if enabled, ok := githubConfig["enabled"].(bool); ok {
		config.Cfg.Github.Enabled = enabled
	}
	if url, ok := githubConfig["url"].(string); ok {
		config.Cfg.Github.URL = url
	}
	if backupUrls, ok := githubConfig["backup_urls"].([]interface{}); ok {
		config.Cfg.Github.BackupURLs = make([]string, len(backupUrls))
		for i, v := range backupUrls {
			if s, ok := v.(string); ok {
				config.Cfg.Github.BackupURLs[i] = s
			}
		}
	}
	if timeoutStr, ok := githubConfig["timeout"].(string); ok {
		if timeout, err := time.ParseDuration(timeoutStr); err == nil {
			config.Cfg.Github.Timeout = timeout
		}
	}
	if retry, ok := githubConfig["retry"].(float64); ok {
		config.Cfg.Github.Retry = int(retry)
	}
	config.CfgMu.Unlock()

	// 返回成功响应
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	response := map[string]string{
		"status":  "success",
		"message": "配置保存成功",
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "返回响应失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
}

// updateGithubConfigNode 更新YAML节点中的github配置
func updateGithubConfigNode(node *yaml.Node, githubConfig map[string]interface{}) error {
	if node.Kind != yaml.DocumentNode || len(node.Content) == 0 {
		return fmt.Errorf("无效的YAML文档节点")
	}

	// 获取根映射节点
	root := node.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("根节点不是映射节点")
	}

	// 查找github节点
	var githubNode *yaml.Node
	for i := 0; i < len(root.Content); i += 2 {
		keyNode := root.Content[i]
		if keyNode.Kind == yaml.ScalarNode && keyNode.Value == "github" {
			githubNode = root.Content[i+1]
			break
		}
	}

	// 如果没有找到github节点，则创建一个
	if githubNode == nil {
		// 创建新的github键节点
		keyNode := &yaml.Node{
			Kind:  yaml.ScalarNode,
			Value: "github",
		}
		// 创建新的github值节点
		githubNode = &yaml.Node{
			Kind: yaml.MappingNode,
		}
		// 添加到根节点
		root.Content = append(root.Content, keyNode, githubNode)
	}

	// 确保github节点是映射节点
	if githubNode.Kind != yaml.MappingNode {
		githubNode.Kind = yaml.MappingNode
		githubNode.Content = []*yaml.Node{}
	}

	// 更新github配置字段
	updateField(githubNode, "enabled", githubConfig["enabled"])
	updateField(githubNode, "url", githubConfig["url"])
	updateField(githubNode, "timeout", githubConfig["timeout"])
	updateField(githubNode, "retry", githubConfig["retry"])

	// 特殊处理backup_urls数组
	if backupUrls, ok := githubConfig["backup_urls"].([]interface{}); ok {
		updateStringArrayField(githubNode, "backup_urls", backupUrls)
	}

	return nil
}

// updateField 更新YAML节点中的字段
func updateField(node *yaml.Node, key string, value interface{}) {
	if value == nil {
		return
	}

	// 查找现有字段
	for i := 0; i < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		if keyNode.Kind == yaml.ScalarNode && keyNode.Value == key {
			// 找到字段，更新值
			valueNode := node.Content[i+1]
			updateValueNode(valueNode, value)
			return
		}
	}

	// 没有找到字段，创建新字段
	keyNode := &yaml.Node{
		Kind:  yaml.ScalarNode,
		Value: key,
	}
	valueNode := createValueNode(value)
	node.Content = append(node.Content, keyNode, valueNode)
}

// updateStringArrayField 更新字符串数组字段
func updateStringArrayField(node *yaml.Node, key string, values []interface{}) {
	// 查找现有字段
	for i := 0; i < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		if keyNode.Kind == yaml.ScalarNode && keyNode.Value == key {
			// 找到字段，更新值
			valueNode := node.Content[i+1]
			updateStringArrayNode(valueNode, values)
			return
		}
	}

	// 没有找到字段，创建新字段
	keyNode := &yaml.Node{
		Kind:  yaml.ScalarNode,
		Value: key,
	}
	valueNode := createStringArrayNode(values)
	node.Content = append(node.Content, keyNode, valueNode)
}

// updateValueNode 更新值节点
func updateValueNode(node *yaml.Node, value interface{}) {
	switch v := value.(type) {
	case bool:
		node.Kind = yaml.ScalarNode
		if v {
			node.Value = "true"
		} else {
			node.Value = "false"
		}
	case string:
		node.Kind = yaml.ScalarNode
		node.Value = v
	case float64:
		node.Kind = yaml.ScalarNode
		node.Value = fmt.Sprintf("%d", int(v))
	}
}

// createValueNode 创建值节点
func createValueNode(value interface{}) *yaml.Node {
	node := &yaml.Node{}
	updateValueNode(node, value)
	return node
}

// updateStringArrayNode 更新字符串数组节点
func updateStringArrayNode(node *yaml.Node, values []interface{}) {
	node.Kind = yaml.SequenceNode
	node.Content = make([]*yaml.Node, len(values))
	for i, v := range values {
		itemNode := &yaml.Node{
			Kind:  yaml.ScalarNode,
			Value: fmt.Sprintf("%v", v),
		}
		node.Content[i] = itemNode
	}
}

// createStringArrayNode 创建字符串数组节点
func createStringArrayNode(values []interface{}) *yaml.Node {
	node := &yaml.Node{
		Kind: yaml.SequenceNode,
	}
	updateStringArrayNode(node, values)
	return node
}