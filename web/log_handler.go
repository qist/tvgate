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

// handleLogEditor 日志配置编辑器页面
func (h *ConfigHandler) handleLogEditor(w http.ResponseWriter, r *http.Request) {
	webPath := h.getWebPath()

	data := map[string]interface{}{
		"title":   "TVGate 日志配置编辑器",
		"webPath": webPath,
	}

	if err := h.renderTemplate(w, r, "log_editor", "templates/log_editor.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleGetLogConfig 获取日志配置
func (h *ConfigHandler) handleGetLogConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	config.CfgMu.RLock()
	logCfg := config.Cfg.Log
	config.CfgMu.RUnlock()

	resp := map[string]interface{}{
		"enabled":     logCfg.Enabled,
		"file":        logCfg.File,
		"maxsize":     logCfg.MaxSizeMB,
		"maxbackups":  logCfg.MaxBackups,
		"maxage":      logCfg.MaxAgeDays,
		"compress":    logCfg.Compress,
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, "序列化日志配置失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
}

// handleSaveLogConfig 保存日志配置
func (h *ConfigHandler) handleSaveLogConfig(w http.ResponseWriter, r *http.Request) {
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

	var logConfig map[string]interface{}
	if err := json.Unmarshal(body, &logConfig); err != nil {
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

	if fullNode.Kind == yaml.DocumentNode && len(fullNode.Content) > 0 {
		doc := fullNode.Content[0]
		if doc.Kind == yaml.MappingNode {
			logFound := false
			for i := 0; i < len(doc.Content); i += 2 {
				keyNode := doc.Content[i]
				if keyNode.Kind == yaml.ScalarNode && keyNode.Value == "log" {
					newLogNode := &yaml.Node{Kind: yaml.MappingNode}

					// enabled
					if enabled, ok := logConfig["enabled"]; ok {
						newLogNode.Content = append(newLogNode.Content,
							&yaml.Node{Kind: yaml.ScalarNode, Value: "enabled"},
							&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", enabled)})
					}
					
					// file
					if file, ok := logConfig["file"]; ok {
						fileStr := fmt.Sprintf("%v", file)
						if fileStr != "" {
							newLogNode.Content = append(newLogNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "file"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: fileStr, Style: yaml.DoubleQuotedStyle})
						}
					}
					
					// maxsize
					if maxSize, ok := logConfig["maxsize"]; ok {
						newLogNode.Content = append(newLogNode.Content,
							&yaml.Node{Kind: yaml.ScalarNode, Value: "maxsize"},
							&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", maxSize)})
					}
					
					// maxbackups
					if maxBackups, ok := logConfig["maxbackups"]; ok {
						newLogNode.Content = append(newLogNode.Content,
							&yaml.Node{Kind: yaml.ScalarNode, Value: "maxbackups"},
							&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", maxBackups)})
					}
					
					// maxage
					if maxAge, ok := logConfig["maxage"]; ok {
						newLogNode.Content = append(newLogNode.Content,
							&yaml.Node{Kind: yaml.ScalarNode, Value: "maxage"},
							&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", maxAge)})
					}
					
					// compress
					if compress, ok := logConfig["compress"]; ok {
						newLogNode.Content = append(newLogNode.Content,
							&yaml.Node{Kind: yaml.ScalarNode, Value: "compress"},
							&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", compress)})
					}

					doc.Content[i+1] = newLogNode
					logFound = true
					break
				}
			}
			if !logFound {
				doc.Content = append(doc.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "log"},
					&yaml.Node{Kind: yaml.MappingNode})
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
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("日志配置保存成功"))
}