package web

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"net/http"

	"github.com/qist/tvgate/config"
	"gopkg.in/yaml.v3"
)

// handleJXEditor 处理jx编辑器页面
func (h *ConfigHandler) handleJXEditor(w http.ResponseWriter, r *http.Request) {
	webPath := h.getWebPath()

	data := map[string]interface{}{
		"title":   "TVGate JX编辑器",
		"webPath": webPath,
	}

	if err := h.renderTemplate(w, r, "jx_editor", "templates/jx_editor.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleJXConfig 处理jx配置获取请求
func (h *ConfigHandler) handleJXConfig(w http.ResponseWriter, r *http.Request) {
	// 设置响应头
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	// 获取当前配置
	config.CfgMu.RLock()
	jx := config.Cfg.JX
	config.CfgMu.RUnlock()

	// 添加日志，打印从内存中读取的配置
	// fmt.Printf("DEBUG: 从内存中读取的JX配置: Path=%s, DefaultID=%s\n", jx.Path, jx.DefaultID)

	// 转换为可JSON序列化的格式
	jxMap := map[string]interface{}{
		"path":       jx.Path,
		"default_id": jx.DefaultID,
	}

	// 添加api_groups配置（如果存在）
	apiGroups := make(map[string]interface{})
	if len(jx.APIGroups) > 0 {
		for groupName, group := range jx.APIGroups {
			groupMap := map[string]interface{}{
				"endpoints":      group.Endpoints,
				"timeout":        formatDuration(group.Timeout),
				"query_template": group.QueryTemplate,
				"primary":        group.Primary,
				"weight":         group.Weight,
				"fallback":       group.Fallback,
				"max_retries":    group.MaxRetries,
				"filters":        group.Filters,
			}
			apiGroups[groupName] = groupMap
		}
	}
	jxMap["api_groups"] = apiGroups

	// 添加日志，打印将要返回给前端的数据
	// fmt.Printf("DEBUG: 将要返回给前端的数据: %+v\n", jxMap)

	// 返回成功响应
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(jxMap)
}

// handleJXConfigSave 处理jx配置保存请求
func (h *ConfigHandler) handleJXConfigSave(w http.ResponseWriter, r *http.Request) {
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

	// 添加日志，打印接收到的原始请求体
	// fmt.Printf("DEBUG: 接收到的原始请求体: %s\n", string(body))

	// 解析JSON数据
	var jxConfig map[string]interface{}
	if err := json.Unmarshal(body, &jxConfig); err != nil {
		http.Error(w, "解析JSON失败: "+err.Error(), http.StatusBadRequest)
		return
	}

	// 添加日志，打印解析后的数据
	// fmt.Printf("DEBUG: 解析后的jxConfig: %+v\n", jxConfig)
	
	// 特别打印 path 和 default_id 字段
	// if path, ok := jxConfig["path"]; ok {
	// 	fmt.Printf("DEBUG: path字段值: %v, 类型: %T\n", path, path)
	// }
	
	// if defaultID, ok := jxConfig["default_id"]; ok {
	// 	fmt.Printf("DEBUG: default_id字段值: %v, 类型: %T\n", defaultID, defaultID)
	// }

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

	// 查找并更新jx节点
	if fullNode.Kind == yaml.DocumentNode && len(fullNode.Content) > 0 {
		root := fullNode.Content[0]
		if root.Kind == yaml.MappingNode {
			// 查找jx节点
			var jxNode *yaml.Node
			for i := 0; i < len(root.Content); i += 2 {
				if root.Content[i].Value == "jx" {
					jxNode = root.Content[i+1]
					break
				}
			}

			// 如果没有找到jx节点，则创建一个新的
			if jxNode == nil {
				// 添加jx键
				root.Content = append(root.Content, &yaml.Node{
					Kind:  yaml.ScalarNode,
					Value: "jx",
				})
				// 添加jx值节点
				jxNode = &yaml.Node{Kind: yaml.MappingNode}
				root.Content = append(root.Content, jxNode)
			} else {
				// 清空现有的jx节点内容
				jxNode.Content = []*yaml.Node{}
			}

			// 添加path字段
			if path, ok := jxConfig["path"]; ok && path != "" {
				// fmt.Printf("DEBUG: 正在设置path字段，值为: %v\n", path)
				jxNode.Content = append(jxNode.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "path"},
					&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", path)})
			}

			// 添加default_id字段
			if defaultID, ok := jxConfig["default_id"]; ok && defaultID != "" {
				// fmt.Printf("DEBUG: 正在设置default_id字段，值为: %v\n", defaultID)
				jxNode.Content = append(jxNode.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "default_id"},
					&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", defaultID)})
			}

			// 添加api_groups字段
			if apiGroups, ok := jxConfig["api_groups"]; ok {
				apiGroupsMap, ok := apiGroups.(map[string]interface{})
				if ok && len(apiGroupsMap) > 0 {
					// 创建api_groups节点
					apiGroupsNode := &yaml.Node{Kind: yaml.MappingNode}
					
					// 遍历api_groups
					for groupName, groupData := range apiGroupsMap {
						groupMap, ok := groupData.(map[string]interface{})
						if !ok {
							continue
						}
						
						// 创建group节点
						groupNode := &yaml.Node{Kind: yaml.MappingNode}
						
						// 添加endpoints字段
						if endpoints, ok := groupMap["endpoints"]; ok {
							endpointsSlice, ok := endpoints.([]interface{})
							if ok && len(endpointsSlice) > 0 {
								endpointsNode := &yaml.Node{Kind: yaml.SequenceNode}
								for _, endpoint := range endpointsSlice {
									endpointsNode.Content = append(endpointsNode.Content, &yaml.Node{
										Kind:  yaml.ScalarNode,
										Value: fmt.Sprintf("%v", endpoint),
									})
								}
								groupNode.Content = append(groupNode.Content,
									&yaml.Node{Kind: yaml.ScalarNode, Value: "endpoints"},
									endpointsNode)
							}
						}
						
						// 添加timeout字段
						if timeout, ok := groupMap["timeout"]; ok && timeout != "" {
							groupNode.Content = append(groupNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "timeout"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", timeout)})
						}
						
						// 添加query_template字段
						if queryTemplate, ok := groupMap["query_template"]; ok && queryTemplate != "" {
							groupNode.Content = append(groupNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "query_template"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", queryTemplate)})
						}
						
						// 添加primary字段
						if primary, ok := groupMap["primary"]; ok {
							groupNode.Content = append(groupNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "primary"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", primary)})
						}
						
						// 添加weight字段
						if weight, ok := groupMap["weight"]; ok {
							groupNode.Content = append(groupNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "weight"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", weight)})
						}
						
						// 添加fallback字段
						if fallback, ok := groupMap["fallback"]; ok {
							groupNode.Content = append(groupNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "fallback"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", fallback)})
						}
						
						// 添加max_retries字段
						if maxRetries, ok := groupMap["max_retries"]; ok {
							groupNode.Content = append(groupNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "max_retries"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", maxRetries)})
						}
						
						// 添加filters字段
						if filters, ok := groupMap["filters"]; ok {
							filtersMap, ok := filters.(map[string]interface{})
							if ok && len(filtersMap) > 0 {
								filtersNode := &yaml.Node{Kind: yaml.MappingNode}
								for filterKey, filterValue := range filtersMap {
									filtersNode.Content = append(filtersNode.Content,
										&yaml.Node{Kind: yaml.ScalarNode, Value: filterKey},
										&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", filterValue)})
								}
								groupNode.Content = append(groupNode.Content,
									&yaml.Node{Kind: yaml.ScalarNode, Value: "filters"},
									filtersNode)
							}
						}
						
						// 将group节点添加到api_groups节点
						apiGroupsNode.Content = append(apiGroupsNode.Content,
							&yaml.Node{Kind: yaml.ScalarNode, Value: groupName},
							groupNode)
					}
					
					// 将api_groups节点添加到jx节点
					jxNode.Content = append(jxNode.Content,
						&yaml.Node{Kind: yaml.ScalarNode, Value: "api_groups"},
						apiGroupsNode)
				}
			}
		}
	}

	// 将更新后的配置写回文件
	output, err := yaml.Marshal(&fullNode)
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
	if err := os.WriteFile(configPath, output, 0644); err != nil {
		// 恢复备份文件
		os.WriteFile(configPath, data, 0644)
		http.Error(w, "写入配置文件失败，已恢复备份: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 返回更新后的配置数据
	config.CfgMu.RLock()
	jx := config.Cfg.JX
	config.CfgMu.RUnlock()

	// 添加日志，打印从内存中读取的配置
	// fmt.Printf("DEBUG: 从内存中读取的JX配置: Path=%s, DefaultID=%s\n", jx.Path, jx.DefaultID)

	// 转换为可JSON序列化的格式
	jxMap := map[string]interface{}{
		"path":       jx.Path,
		"default_id": jx.DefaultID,
	}

	// 添加api_groups配置（如果存在）
	apiGroups := make(map[string]interface{})
	if len(jx.APIGroups) > 0 {
		for groupName, group := range jx.APIGroups {
			groupMap := map[string]interface{}{
				"endpoints":      group.Endpoints,
				"timeout":        formatDuration(group.Timeout),
				"query_template": group.QueryTemplate,
				"primary":        group.Primary,
				"weight":         group.Weight,
				"fallback":       group.Fallback,
				"max_retries":    group.MaxRetries,
				"filters":        group.Filters,
			}
			apiGroups[groupName] = groupMap
		}
	}
	jxMap["api_groups"] = apiGroups

	// 添加日志，打印将要返回给前端的数据
	// fmt.Printf("DEBUG: 将要返回给前端的数据: %+v\n", jxMap)

	// 返回成功响应
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(jxMap)
}