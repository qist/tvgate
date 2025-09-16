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

// handleServerMonitorEditor 处理服务器监控编辑器页面
func (h *ConfigHandler) handleServerMonitorEditor(w http.ResponseWriter, r *http.Request) {
	webPath := h.getWebPath()
	
	// 获取监控路径
	config.CfgMu.RLock()
	monitorPath := config.Cfg.Monitor.Path
	config.CfgMu.RUnlock()
	
	// 如果监控路径为空，使用默认值
	if monitorPath == "" {
		monitorPath = "/status"
	}

	data := map[string]interface{}{
		"title":      "TVGate 服务器监控编辑器",
		"webPath":    webPath,
		"monitorPath": monitorPath,
	}

	if err := h.renderTemplate(w, r, "server_monitor_editor", "templates/server_monitor_editor.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleServerMonitorConfig 处理服务器监控配置获取请求
func (h *ConfigHandler) handleServerMonitorConfig(w http.ResponseWriter, r *http.Request) {
	// 设置响应头
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	// 获取当前配置
	config.CfgMu.RLock()
	serverConfig := map[string]interface{}{
		"monitor_path": config.Cfg.Monitor.Path,
	}
	config.CfgMu.RUnlock()

	// 返回JSON格式的配置
	if err := json.NewEncoder(w).Encode(serverConfig); err != nil {
		http.Error(w, "序列化配置失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
}

// handleServerMonitorConfigSave 处理服务器监控配置保存请求
func (h *ConfigHandler) handleServerMonitorConfigSave(w http.ResponseWriter, r *http.Request) {
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
	var serverConfig map[string]interface{}
	if err := json.Unmarshal(body, &serverConfig); err != nil {
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

	// 查找并更新monitor相关配置
	if fullNode.Kind == yaml.DocumentNode && len(fullNode.Content) > 0 {
		doc := fullNode.Content[0]
		if doc.Kind == yaml.MappingNode {
			// 查找monitor节点并更新
			for i := 0; i < len(doc.Content); i += 2 {
				keyNode := doc.Content[i]
				if keyNode.Kind == yaml.ScalarNode && keyNode.Value == "monitor" {
					monitorNode := doc.Content[i+1]
					if monitorNode.Kind == yaml.MappingNode {
						// 查找并更新path字段
						pathFound := false
						for j := 0; j < len(monitorNode.Content); j += 2 {
							fieldKey := monitorNode.Content[j]
							if fieldKey.Kind == yaml.ScalarNode && fieldKey.Value == "path" {
								// 更新path字段
								if pathValue, ok := serverConfig["monitor_path"]; ok && pathValue != "" {
									monitorNode.Content[j+1] = &yaml.Node{
										Kind:  yaml.ScalarNode,
										Value: fmt.Sprintf("%v", pathValue),
									}
									pathFound = true
									break
								}
							}
						}
								
						// 如果没有找到path字段，则添加
						if !pathFound {
							if pathValue, ok := serverConfig["monitor_path"]; ok {
								monitorNode.Content = append(monitorNode.Content,
									&yaml.Node{Kind: yaml.ScalarNode, Value: "path"},
									&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", pathValue)})
							}
						} else {
							// 如果找到了path字段，更新它（即使值为空）
							for j := 0; j < len(monitorNode.Content); j += 2 {
								fieldKey := monitorNode.Content[j]
								if fieldKey.Kind == yaml.ScalarNode && fieldKey.Value == "path" {
									if pathValue, ok := serverConfig["monitor_path"]; ok {
										monitorNode.Content[j+1] = &yaml.Node{
											Kind:  yaml.ScalarNode,
											Value: fmt.Sprintf("%v", pathValue),
										}
									}
									break
								}
							}
						}
					}
				}
				
			}
		}
	}

	// 序列化为YAML格式
	newData, err := yaml.Marshal(&fullNode)
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

	// 写入新配置
	if err := os.WriteFile(configPath, newData, 0644); err != nil {
		// 恢复备份文件
		os.WriteFile(configPath, data, 0644)
		http.Error(w, "写入配置文件失败，已恢复备份: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 返回成功响应
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("配置保存成功"))
}