package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/qist/tvgate/config"
	"gopkg.in/yaml.v3"
)

// handleGlobalAuthEditor 处理全局认证编辑器页面
func (h *ConfigHandler) handleGlobalAuthEditor(w http.ResponseWriter, r *http.Request) {
	webPath := h.getWebPath()

	data := map[string]interface{}{
		"title":   "TVGate 全局认证编辑器",
		"webPath": webPath,
	}

	if err := h.renderTemplate(w, r, "global_auth_editor", "templates/global_auth_editor.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleGlobalAuthConfig 处理全局认证配置获取请求
func (h *ConfigHandler) handleGlobalAuthConfig(w http.ResponseWriter, r *http.Request) {
	// 设置响应头
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	// 获取当前配置
	config.CfgMu.RLock()
	globalAuth := config.Cfg.GlobalAuth
	config.CfgMu.RUnlock()

	// 转换为可JSON序列化的格式
	authConfig := map[string]interface{}{
		"tokens_enabled":   globalAuth.TokensEnabled,
		"token_param_name": globalAuth.TokenParamName,
		"dynamic_tokens": map[string]interface{}{
			"enable_dynamic": globalAuth.DynamicTokens.EnableDynamic,
			"dynamic_ttl":    formatDuration(globalAuth.DynamicTokens.DynamicTTL),
			"secret":         globalAuth.DynamicTokens.Secret,
			"salt":           globalAuth.DynamicTokens.Salt,
		},
		"static_tokens": map[string]interface{}{
			"enable_static": globalAuth.StaticTokens.EnableStatic,
			"token":         globalAuth.StaticTokens.Token,
			"expire_hours":  formatDuration(globalAuth.StaticTokens.ExpireHours),
		},
	}

	// 返回JSON格式的配置
	if err := json.NewEncoder(w).Encode(authConfig); err != nil {
		http.Error(w, "序列化配置失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
}

// formatDurationString 格式化时间字符串，去除末尾的0部分
func formatDurationString(durationStr string) string {
	if durationStr == "" {
		return ""
	}

	// 移除末尾的0部分
	result := durationStr
	// 移除末尾的0s
	result = strings.TrimSuffix(result, "0s")
	// 移除末尾的0m
	result = strings.TrimSuffix(result, "0m")
	// 移除末尾的0h
	result = strings.TrimSuffix(result, "0h")
	// 处理特殊情况，如1m0s变成1m
	result = strings.ReplaceAll(result, "m0s", "m")
	// 处理特殊情况，如1h0m变成1h
	result = strings.ReplaceAll(result, "h0m", "h")
	// 处理特殊情况，如1h0s变成1h
	result = strings.ReplaceAll(result, "h0s", "h")

	return result
}

// handleGlobalAuthConfigSave 处理全局认证配置保存请求
func (h *ConfigHandler) handleGlobalAuthConfigSave(w http.ResponseWriter, r *http.Request) {
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
	var authConfig map[string]interface{}
	if err := json.Unmarshal(body, &authConfig); err != nil {
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

	// 查找并更新global_auth节点
	if fullNode.Kind == yaml.DocumentNode && len(fullNode.Content) > 0 {
		doc := fullNode.Content[0]
		if doc.Kind == yaml.MappingNode {
			// 查找global_auth节点
			globalAuthFound := false
			for i := 0; i < len(doc.Content); i += 2 {
				keyNode := doc.Content[i]
				if keyNode.Kind == yaml.ScalarNode && keyNode.Value == "global_auth" {
					// 创建新的global_auth节点
					newAuthNode := &yaml.Node{Kind: yaml.MappingNode}

					// 添加tokens_enabled
					if tokensEnabled, ok := authConfig["tokens_enabled"]; ok {
						newAuthNode.Content = append(newAuthNode.Content,
							&yaml.Node{Kind: yaml.ScalarNode, Value: "tokens_enabled"},
							&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", tokensEnabled)})
					}

					// 添加token_param_name
					if tokenParamName, ok := authConfig["token_param_name"]; ok && tokenParamName != "" {
						newAuthNode.Content = append(newAuthNode.Content,
							&yaml.Node{Kind: yaml.ScalarNode, Value: "token_param_name"},
							&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", tokenParamName)})
					}

					// 添加dynamic_tokens
					if dynamicTokens, ok := authConfig["dynamic_tokens"]; ok {
						if dtMap, ok := dynamicTokens.(map[string]interface{}); ok {
							dtNode := &yaml.Node{Kind: yaml.MappingNode}

							if enableDynamic, ok := dtMap["enable_dynamic"]; ok {
								dtNode.Content = append(dtNode.Content,
									&yaml.Node{Kind: yaml.ScalarNode, Value: "enable_dynamic"},
									&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", enableDynamic)})
							}

							if dynamicTTL, ok := dtMap["dynamic_ttl"]; ok && dynamicTTL != "" {
								// 解析时间字符串
								if ttlStr, ok := dynamicTTL.(string); ok && ttlStr != "" {
									// 格式化时间字符串
									formattedTTL := formatDurationString(ttlStr)
									if _, err := time.ParseDuration(formattedTTL); err == nil {
										dtNode.Content = append(dtNode.Content,
											&yaml.Node{Kind: yaml.ScalarNode, Value: "dynamic_ttl"},
											&yaml.Node{Kind: yaml.ScalarNode, Value: formattedTTL})
									}
								}
							}

							if secret, ok := dtMap["secret"]; ok && secret != "" {
								dtNode.Content = append(dtNode.Content,
									&yaml.Node{Kind: yaml.ScalarNode, Value: "secret"},
									&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", secret)})
							}

							if salt, ok := dtMap["salt"]; ok && salt != "" {
								dtNode.Content = append(dtNode.Content,
									&yaml.Node{Kind: yaml.ScalarNode, Value: "salt"},
									&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", salt)})
							}

							if len(dtNode.Content) > 0 {
								newAuthNode.Content = append(newAuthNode.Content,
									&yaml.Node{Kind: yaml.ScalarNode, Value: "dynamic_tokens"},
									dtNode)
							}
						}
					}

					// 添加static_tokens
					if staticTokens, ok := authConfig["static_tokens"]; ok {
						if stMap, ok := staticTokens.(map[string]interface{}); ok {
							stNode := &yaml.Node{Kind: yaml.MappingNode}

							if enableStatic, ok := stMap["enable_static"]; ok {
								stNode.Content = append(stNode.Content,
									&yaml.Node{Kind: yaml.ScalarNode, Value: "enable_static"},
									&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", enableStatic)})
							}

							if token, ok := stMap["token"]; ok && token != "" {
								stNode.Content = append(stNode.Content,
									&yaml.Node{Kind: yaml.ScalarNode, Value: "token"},
									&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", token)})
							}

							if expireHours, ok := stMap["expire_hours"]; ok && expireHours != "" {
								// 解析时间字符串
								if expireStr, ok := expireHours.(string); ok && expireStr != "" {
									// 格式化时间字符串
									formattedExpire := formatDurationString(expireStr)
									if _, err := time.ParseDuration(formattedExpire); err == nil {
										stNode.Content = append(stNode.Content,
											&yaml.Node{Kind: yaml.ScalarNode, Value: "expire_hours"},
											&yaml.Node{Kind: yaml.ScalarNode, Value: formattedExpire})
									}
								}
							}

							if len(stNode.Content) > 0 {
								newAuthNode.Content = append(newAuthNode.Content,
									&yaml.Node{Kind: yaml.ScalarNode, Value: "static_tokens"},
									stNode)
							}
						}
					}

					// 替换global_auth节点
					doc.Content[i+1] = newAuthNode
					globalAuthFound = true
					break
				}
			}

			// 如果没有找到global_auth节点，则创建一个新的
			if !globalAuthFound {
				doc.Content = append(doc.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "global_auth"},
					&yaml.Node{Kind: yaml.MappingNode})
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

// formatDuration 格式化时间持续时间，如果为0则返回空字符串
func formatDuration(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return d.String()
}
