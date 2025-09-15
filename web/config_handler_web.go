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

// handleWebEditor 处理 Web 配置编辑器页面
func (h *ConfigHandler) handleWebEditor(w http.ResponseWriter, r *http.Request) {
	webPath := h.getWebPath()

	data := map[string]interface{}{
		"title":   "TVGate Web 配置编辑器",
		"webPath": webPath,
	}

	if err := h.renderTemplate(w, r, "web_editor", "templates/web_editor.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleWebConfig 获取当前 Web 配置
func (h *ConfigHandler) handleWebConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	config.CfgMu.RLock()
	webCfg := config.Cfg.Web
	config.CfgMu.RUnlock()

	resp := map[string]interface{}{
		"enabled":  webCfg.Enabled,
		"username": webCfg.Username,
		"password": webCfg.Password,
		"path":     webCfg.Path,
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, "序列化配置失败: "+err.Error(), http.StatusInternalServerError)
	}
}

// handleWebConfigSave 保存 Web 配置
func (h *ConfigHandler) handleWebConfigSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "读取请求体失败: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var webConfig map[string]interface{}
	if err := json.Unmarshal(body, &webConfig); err != nil {
		http.Error(w, "解析JSON失败: "+err.Error(), http.StatusBadRequest)
		return
	}

	configPath := *config.ConfigFilePath
	data, err := os.ReadFile(configPath)
	if err != nil {
		http.Error(w, "读取配置文件失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var fullNode yaml.Node
	if err := yaml.Unmarshal(data, &fullNode); err != nil {
		http.Error(w, "解析配置文件失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 查找并更新 web 节点
	if fullNode.Kind == yaml.DocumentNode && len(fullNode.Content) > 0 {
		doc := fullNode.Content[0]
		if doc.Kind == yaml.MappingNode {
			webFound := false
			for i := 0; i < len(doc.Content); i += 2 {
				keyNode := doc.Content[i]
				if keyNode.Kind == yaml.ScalarNode && keyNode.Value == "web" {
					newWebNode := &yaml.Node{Kind: yaml.MappingNode}

					if enabled, ok := webConfig["enabled"]; ok {
						newWebNode.Content = append(newWebNode.Content,
							&yaml.Node{Kind: yaml.ScalarNode, Value: "enabled"},
							&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", enabled)})
					}
					if username, ok := webConfig["username"]; ok {
						newWebNode.Content = append(newWebNode.Content,
							&yaml.Node{Kind: yaml.ScalarNode, Value: "username"},
							&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", username)})
					}
					if password, ok := webConfig["password"]; ok {
						newWebNode.Content = append(newWebNode.Content,
							&yaml.Node{Kind: yaml.ScalarNode, Value: "password"},
							&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", password)})
					}
					if path, ok := webConfig["path"]; ok {
						newWebNode.Content = append(newWebNode.Content,
							&yaml.Node{Kind: yaml.ScalarNode, Value: "path"},
							&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", path)})
					}

					doc.Content[i+1] = newWebNode
					webFound = true
					break
				}
			}

			// 没找到 web 节点就新建
			if !webFound {
				newWebNode := &yaml.Node{Kind: yaml.MappingNode}
				doc.Content = append(doc.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "web"},
					newWebNode)
			}
		}
	}

	newData, err := yaml.Marshal(&fullNode)
	if err != nil {
		http.Error(w, "序列化配置失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	backupPath := configPath + ".backup." + time.Now().Format("20060102150405")
	if err := os.WriteFile(backupPath, data, 0644); err != nil {
		http.Error(w, "创建备份文件失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err := os.WriteFile(configPath, newData, 0644); err != nil {
		os.WriteFile(configPath, data, 0644)
		http.Error(w, "写入配置失败，已恢复备份: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte("配置保存成功"))
}
