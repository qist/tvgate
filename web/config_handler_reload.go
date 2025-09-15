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

// handleReloadEditor 显示 reload 编辑器页面
func (h *ConfigHandler) handleReloadEditor(w http.ResponseWriter, r *http.Request) {
	webPath := h.getWebPath()

	data := map[string]interface{}{
		"title":   "TVGate 配置重新加载间隔",
		"webPath": webPath,
	}

	if err := h.renderTemplate(w, r, "reload_editor", "templates/reload_editor.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleReloadConfig 获取当前 reload 配置
func (h *ConfigHandler) handleReloadConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	config.CfgMu.RLock()
	reload := config.Cfg.Reload
	config.CfgMu.RUnlock()

	resp := map[string]interface{}{
		"reload": reload,
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, "序列化配置失败: "+err.Error(), http.StatusInternalServerError)
	}
}

// handleReloadConfigSave 保存 reload 配置
func (h *ConfigHandler) handleReloadConfigSave(w http.ResponseWriter, r *http.Request) {
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

	var cfg map[string]interface{}
	if err := json.Unmarshal(body, &cfg); err != nil {
		http.Error(w, "解析JSON失败: "+err.Error(), http.StatusBadRequest)
		return
	}

	reloadVal, ok := cfg["reload"]
	if !ok {
		http.Error(w, "缺少 reload 参数", http.StatusBadRequest)
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

	// 查找并更新 reload 字段
	if fullNode.Kind == yaml.DocumentNode && len(fullNode.Content) > 0 {
		doc := fullNode.Content[0]
		if doc.Kind == yaml.MappingNode {
			found := false
			for i := 0; i < len(doc.Content); i += 2 {
				keyNode := doc.Content[i]
				if keyNode.Kind == yaml.ScalarNode && keyNode.Value == "reload" {
					doc.Content[i+1].Value = fmt.Sprintf("%v", reloadVal)
					found = true
					break
				}
			}
			if !found {
				doc.Content = append(doc.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "reload"},
					&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", reloadVal)})
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
