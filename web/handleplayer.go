package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/qist/tvgate/config"
	"gopkg.in/yaml.v3"
)

// handlePlayerEditor 显示播放器模块配置编辑器页面
func (h *ConfigHandler) handlePlayerEditor(w http.ResponseWriter, r *http.Request) {
	webPath := h.getWebPath()
	data := map[string]interface{}{
		"title":   "TVGate H5 播放器配置",
		"webPath": webPath,
	}
	if err := h.renderTemplate(w, r, "player_editor", "templates/player_editor.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handlePlayerConfig 获取当前播放器模块配置
func (h *ConfigHandler) handlePlayerConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	config.CfgMu.RLock()
	p := config.Cfg.Player
	config.CfgMu.RUnlock()
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"enabled":         p.Enabled,
		"subscription":    p.Subscription,
		"epg":             p.Epg,
		"logo":            p.Logo,
		"logo_dir":        p.LogoDir,
		"update_interval": p.UpdateInterval.String(),
	})
}

// handlePlayerConfigSave 保存播放器模块配置
func (h *ConfigHandler) handlePlayerConfigSave(w http.ResponseWriter, r *http.Request) {
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
			found := false
			for i := 0; i < len(doc.Content); i += 2 {
				keyNode := doc.Content[i]
				if keyNode.Kind == yaml.ScalarNode && keyNode.Value == "player" {
					doc.Content[i+1] = buildPlayerNode(cfg)
					found = true
					break
				}
			}
			if !found {
				doc.Content = append(doc.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "player"},
					buildPlayerNode(cfg))
			}
		}
	}

	saveAndResponse(w, configPath, data, &fullNode)
}

// buildPlayerNode 根据前端提交的配置构建 player YAML 节点
func buildPlayerNode(cfg map[string]interface{}) *yaml.Node {
	node := &yaml.Node{Kind: yaml.MappingNode}
	if v, ok := cfg["enabled"]; ok {
		val := "false"
		if e, ok := v.(bool); ok && e {
			val = "true"
		}
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "enabled"},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: val})
	}
	if v, ok := cfg["subscription"]; ok {
		s := strings.TrimSpace(fmt.Sprintf("%v", v))
		if s != "" {
			node.Content = append(node.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "subscription"},
				&yaml.Node{Kind: yaml.ScalarNode, Value: s})
		}
	}
	if v, ok := cfg["epg"]; ok {
		s := strings.TrimSpace(fmt.Sprintf("%v", v))
		if s != "" {
			node.Content = append(node.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "epg"},
				&yaml.Node{Kind: yaml.ScalarNode, Value: s})
		}
	}
	if v, ok := cfg["logo"]; ok {
		s := strings.TrimSpace(fmt.Sprintf("%v", v))
		if s != "" {
			node.Content = append(node.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "logo"},
				&yaml.Node{Kind: yaml.ScalarNode, Value: s})
		}
	}
	if v, ok := cfg["logo_dir"]; ok {
		s := strings.TrimSpace(fmt.Sprintf("%v", v))
		if s != "" {
			node.Content = append(node.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "logo_dir"},
				&yaml.Node{Kind: yaml.ScalarNode, Value: s})
		}
	}
	if v, ok := cfg["update_interval"]; ok {
		s := strings.TrimSpace(fmt.Sprintf("%v", v))
		if s != "" && s != "2h0m0s" && s != "0s" {
			node.Content = append(node.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "update_interval"},
				&yaml.Node{Kind: yaml.ScalarNode, Value: s})
		}
	}
	return node
}
