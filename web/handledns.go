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

// handleDnsEditor 处理 DNS 配置编辑器页面
func (h *ConfigHandler) handleDnsEditor(w http.ResponseWriter, r *http.Request) {
	webPath := h.getWebPath()

	data := map[string]interface{}{
		"title":   "TVGate DNS 配置编辑器",
		"webPath": webPath,
	}

	if err := h.renderTemplate(w, r, "dns_editor", "templates/dns_editor.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleDnsConfig 处理 DNS 配置获取请求
func (h *ConfigHandler) handleDnsConfig(w http.ResponseWriter, r *http.Request) {
	// 设置响应头
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	// 获取当前配置
	config.CfgMu.RLock()
	dns := config.Cfg.DNS
	config.CfgMu.RUnlock()

	// 转换为可JSON序列化的格式
	dnsConfig := map[string]interface{}{
		"servers":   dns.Servers,
		"timeout":   formatDuration(dns.Timeout),
		"max_conns": dns.MaxConns,
	}

	// 返回JSON格式的配置
	if err := json.NewEncoder(w).Encode(dnsConfig); err != nil {
		http.Error(w, "序列化配置失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
}

// handleDnsConfigSave 处理 DNS 配置保存请求
func (h *ConfigHandler) handleDnsConfigSave(w http.ResponseWriter, r *http.Request) {
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
	var dnsConfig map[string]interface{}
	if err := json.Unmarshal(body, &dnsConfig); err != nil {
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

	// 查找并更新dns配置节点
	if err := updateDnsConfigNode(&fullNode, dnsConfig); err != nil {
		http.Error(w, "更新配置失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 序列化更新后的配置
	updatedData, err := yaml.Marshal(&fullNode)
	if err != nil {
		http.Error(w, "序列化配置失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 创建备份文件
	backupPath := configPath + ".backup." + time.Now().Format("20060102150405")
	if err := os.WriteFile(backupPath, data, 0644); err != nil {
		http.Error(w, "创建备份文件失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 写入配置文件
	if err := os.WriteFile(configPath, updatedData, 0644); err != nil {
		http.Error(w, "写入配置文件失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 更新内存中的配置
	config.CfgMu.Lock()
	if servers, ok := dnsConfig["servers"].([]interface{}); ok {
		config.Cfg.DNS.Servers = make([]string, len(servers))
		for i, v := range servers {
			if s, ok := v.(string); ok {
				config.Cfg.DNS.Servers[i] = s
			}
		}
	}
	if timeoutStr, ok := dnsConfig["timeout"].(string); ok {
		if timeout, err := time.ParseDuration(timeoutStr); err == nil {
			config.Cfg.DNS.Timeout = timeout
		}
	}
	if maxConns, ok := dnsConfig["max_conns"].(float64); ok {
		config.Cfg.DNS.MaxConns = int(maxConns)
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

// updateDnsConfigNode 更新YAML节点中的dns配置
func updateDnsConfigNode(node *yaml.Node, dnsConfig map[string]interface{}) error {
	if node.Kind != yaml.DocumentNode || len(node.Content) == 0 {
		return fmt.Errorf("无效的YAML文档节点")
	}

	// 获取根映射节点
	root := node.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("根节点不是映射节点")
	}

	// 查找dns节点
	var dnsNode *yaml.Node
	for i := 0; i < len(root.Content); i += 2 {
		keyNode := root.Content[i]
		if keyNode.Kind == yaml.ScalarNode && keyNode.Value == "dns" {
			dnsNode = root.Content[i+1]
			break
		}
	}

	// 如果没有找到dns节点，则创建一个
	if dnsNode == nil {
		// 创建新的dns键节点
		keyNode := &yaml.Node{
			Kind:  yaml.ScalarNode,
			Value: "dns",
		}
		// 创建新的dns值节点
		dnsNode = &yaml.Node{
			Kind: yaml.MappingNode,
		}
		// 添加到根节点
		root.Content = append(root.Content, keyNode, dnsNode)
	}

	// 确保dns节点是映射节点
	if dnsNode.Kind != yaml.MappingNode {
		dnsNode.Kind = yaml.MappingNode
		dnsNode.Content = []*yaml.Node{}
	}

	// 更新dns配置字段
	updateField(dnsNode, "timeout", dnsConfig["timeout"])
	updateField(dnsNode, "max_conns", dnsConfig["max_conns"])

	// 特殊处理servers数组
	if servers, ok := dnsConfig["servers"].([]interface{}); ok {
		updateStringArrayField(dnsNode, "servers", servers)
	}

	return nil
}
