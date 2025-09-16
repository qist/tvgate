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

// handleDomainMapEditor 处理域名映射编辑器页面
func (h *ConfigHandler) handleDomainMapEditor(w http.ResponseWriter, r *http.Request) {
	webPath := h.getWebPath()

	// 检查是否有任何domainmap配置了auth
	config.CfgMu.RLock()
	hasAuthConfig := false
	for _, dm := range config.Cfg.DomainMap {
		if dm.Auth.TokensEnabled ||
			dm.Auth.TokenParamName != "" ||
			dm.Auth.DynamicTokens.EnableDynamic ||
			dm.Auth.DynamicTokens.Secret != "" ||
			dm.Auth.DynamicTokens.Salt != "" ||
			dm.Auth.StaticTokens.EnableStatic ||
			dm.Auth.StaticTokens.Token != "" {
			hasAuthConfig = true
			break
		}
	}
	config.CfgMu.RUnlock()

	data := map[string]interface{}{
		"title":         "TVGate 域名映射编辑器",
		"webPath":       webPath,
		"hasAuthConfig": hasAuthConfig,
	}

	if err := h.renderTemplate(w, r, "domainmap_editor", "templates/domainmap_editor.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleDomainMapConfig 处理域名映射配置获取请求
func (h *ConfigHandler) handleDomainMapConfig(w http.ResponseWriter, r *http.Request) {
	// 设置响应头
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	// 获取当前配置
	config.CfgMu.RLock()
	domainMaps := config.Cfg.DomainMap
	config.CfgMu.RUnlock()

	// 转换为可JSON序列化的格式
	domainMapList := make([]map[string]interface{}, len(domainMaps))
	for i, dm := range domainMaps {
		dmMap := map[string]interface{}{
			"name":     dm.Name,
			"source":   dm.Source,
			"target":   dm.Target,
			"protocol": dm.Protocol,
		}

		// 添加auth配置（如果存在）
		if dm.Auth.TokensEnabled ||
			dm.Auth.TokenParamName != "" ||
			dm.Auth.DynamicTokens.EnableDynamic ||
			dm.Auth.DynamicTokens.Secret != "" ||
			dm.Auth.DynamicTokens.Salt != "" ||
			dm.Auth.StaticTokens.EnableStatic ||
			dm.Auth.StaticTokens.Token != "" {

			dmMap["auth"] = map[string]interface{}{
				"tokens_enabled":   dm.Auth.TokensEnabled,
				"token_param_name": dm.Auth.TokenParamName,
				"dynamic_tokens": map[string]interface{}{
					"enable_dynamic": dm.Auth.DynamicTokens.EnableDynamic,
					"dynamic_ttl":    formatDuration(dm.Auth.DynamicTokens.DynamicTTL),
					"secret":         dm.Auth.DynamicTokens.Secret,
					"salt":           dm.Auth.DynamicTokens.Salt,
				},
				"static_tokens": map[string]interface{}{
					"enable_static": dm.Auth.StaticTokens.EnableStatic,
					"token":         dm.Auth.StaticTokens.Token,
					"expire_hours":  formatDuration(dm.Auth.StaticTokens.ExpireHours),
				},
			}
		}

		// 添加client_headers和server_headers
		if len(dm.ClientHeaders) > 0 {
			dmMap["client_headers"] = dm.ClientHeaders
		}
		if len(dm.ServerHeaders) > 0 {
			dmMap["server_headers"] = dm.ServerHeaders
		}

		domainMapList[i] = dmMap
	}

	// 返回JSON格式的配置
	if err := json.NewEncoder(w).Encode(domainMapList); err != nil {
		http.Error(w, "序列化配置失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
}

// handleDomainMapConfigSave 处理域名映射配置保存请求
func (h *ConfigHandler) handleDomainMapConfigSave(w http.ResponseWriter, r *http.Request) {
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
	var domainMaps []map[string]interface{}
	if err := json.Unmarshal(body, &domainMaps); err != nil {
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

	// 转换域名映射数据
	yamlDomainMaps := make([]*yaml.Node, len(domainMaps))
	for i, dm := range domainMaps {
		domainMapNode := &yaml.Node{Kind: yaml.MappingNode}

		// 添加基本字段
		domainMapNode.Content = append(domainMapNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "name"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", dm["name"])},
			&yaml.Node{Kind: yaml.ScalarNode, Value: "source"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", dm["source"])},
			&yaml.Node{Kind: yaml.ScalarNode, Value: "target"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", dm["target"])},
		)

		// 添加client_headers
		if clientHeaders, ok := dm["client_headers"].(map[string]interface{}); ok && len(clientHeaders) > 0 {
			headersNode := &yaml.Node{Kind: yaml.MappingNode}
			for k, v := range clientHeaders {
				headersNode.Content = append(headersNode.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: k},
					&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", v)},
				)
			}
			domainMapNode.Content = append(domainMapNode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "client_headers"},
				headersNode,
			)
		}

		// 添加server_headers
		if serverHeaders, ok := dm["server_headers"].(map[string]interface{}); ok && len(serverHeaders) > 0 {
			headersNode := &yaml.Node{Kind: yaml.MappingNode}
			for k, v := range serverHeaders {
				headersNode.Content = append(headersNode.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: k},
					&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", v)},
				)
			}
			domainMapNode.Content = append(domainMapNode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "server_headers"},
				headersNode,
			)
		}

		// 添加协议字段（如果存在且非空）
		if protocol, ok := dm["protocol"]; ok && protocol != "" && protocol != nil {
			domainMapNode.Content = append(domainMapNode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "protocol"},
				&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", protocol)},
			)
		}

		// 添加auth字段（如果存在）
		if auth, ok := dm["auth"]; ok && auth != nil {
			if authMap, ok := auth.(map[string]interface{}); ok {
				authNode := &yaml.Node{Kind: yaml.MappingNode}

				// 添加tokens_enabled
				if tokensEnabled, ok := authMap["tokens_enabled"]; ok {
					authNode.Content = append(authNode.Content,
						&yaml.Node{Kind: yaml.ScalarNode, Value: "tokens_enabled"},
						&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", tokensEnabled)},
					)
				}

				// 添加token_param_name
				if tokenParamName, ok := authMap["token_param_name"]; ok && tokenParamName != "" {
					authNode.Content = append(authNode.Content,
						&yaml.Node{Kind: yaml.ScalarNode, Value: "token_param_name"},
						&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", tokenParamName)},
					)
				}

				// 添加dynamic_tokens
				if dynamicTokens, ok := authMap["dynamic_tokens"]; ok {
					if dtMap, ok := dynamicTokens.(map[string]interface{}); ok {
						dtNode := &yaml.Node{Kind: yaml.MappingNode}

						if enableDynamic, ok := dtMap["enable_dynamic"]; ok {
							dtNode.Content = append(dtNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "enable_dynamic"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", enableDynamic)},
							)
						}

						if dynamicTTL, ok := dtMap["dynamic_ttl"]; ok && dynamicTTL != "" {
							// 解析时间字符串
							if ttlStr, ok := dynamicTTL.(string); ok && ttlStr != "" {
								// 格式化时间字符串
								formattedTTL := formatDurationString(ttlStr)
								if _, err := time.ParseDuration(formattedTTL); err == nil {
									dtNode.Content = append(dtNode.Content,
										&yaml.Node{Kind: yaml.ScalarNode, Value: "dynamic_ttl"},
										&yaml.Node{Kind: yaml.ScalarNode, Value: formattedTTL},
									)
								}
							}
						}

						if secret, ok := dtMap["secret"]; ok && secret != "" {
							dtNode.Content = append(dtNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "secret"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", secret)},
							)
						}

						if salt, ok := dtMap["salt"]; ok && salt != "" {
							dtNode.Content = append(dtNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "salt"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", salt)},
							)
						}

						if len(dtNode.Content) > 0 {
							authNode.Content = append(authNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "dynamic_tokens"},
								dtNode,
							)
						}
					}
				}

				// 添加static_tokens
				if staticTokens, ok := authMap["static_tokens"]; ok {
					if stMap, ok := staticTokens.(map[string]interface{}); ok {
						stNode := &yaml.Node{Kind: yaml.MappingNode}

						if enableStatic, ok := stMap["enable_static"]; ok {
							stNode.Content = append(stNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "enable_static"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", enableStatic)},
							)
						}

						if token, ok := stMap["token"]; ok && token != "" {
							stNode.Content = append(stNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "token"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", token)},
							)
						}

						if expireHours, ok := stMap["expire_hours"]; ok && expireHours != "" {
							// 解析时间字符串
							if expireStr, ok := expireHours.(string); ok && expireStr != "" {
								// 格式化时间字符串
								formattedExpire := formatDurationString(expireStr)
								if _, err := time.ParseDuration(formattedExpire); err == nil {
									stNode.Content = append(stNode.Content,
										&yaml.Node{Kind: yaml.ScalarNode, Value: "expire_hours"},
										&yaml.Node{Kind: yaml.ScalarNode, Value: formattedExpire},
									)
								}
							}
						}

						if len(stNode.Content) > 0 {
							authNode.Content = append(authNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "static_tokens"},
								stNode,
							)
						}
					}
				}

				if len(authNode.Content) > 0 {
					domainMapNode.Content = append(domainMapNode.Content,
						&yaml.Node{Kind: yaml.ScalarNode, Value: "auth"},
						authNode,
					)
				}
			}
		}

		yamlDomainMaps[i] = domainMapNode
	}

	// 更新域名映射配置，保持原有结构
	if fullNode.Kind == yaml.DocumentNode && len(fullNode.Content) > 0 {
		doc := fullNode.Content[0]
		if doc.Kind == yaml.MappingNode {
			// 查找domainmap节点并更新
			for i := 0; i < len(doc.Content); i += 2 {
				keyNode := doc.Content[i]
				if keyNode.Kind == yaml.ScalarNode && keyNode.Value == "domainmap" {
					// 创建新的序列节点
					seqNode := &yaml.Node{Kind: yaml.SequenceNode}
					seqNode.Content = yamlDomainMaps
					// 替换值节点
					doc.Content[i+1] = seqNode
					break
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
