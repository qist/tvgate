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

// handlePHPEditor 显示 PHP 模块配置编辑器页面
func (h *ConfigHandler) handlePHPEditor(w http.ResponseWriter, r *http.Request) {
	webPath := h.getWebPath()

	data := map[string]interface{}{
		"title":   "TVGate PHP 模块配置编辑器",
		"webPath": webPath,
	}

	if err := h.renderTemplate(w, r, "php_editor", "templates/php_editor.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handlePHPConfig 获取当前 PHP 模块配置
func (h *ConfigHandler) handlePHPConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	config.CfgMu.RLock()
	php := config.Cfg.PHP
	config.CfgMu.RUnlock()

	resp := map[string]interface{}{
		"enabled":     php.Enabled,
		"path":        php.Path,
		"docroot":     php.DocRoot,
		"index":       php.Index,
		"worker_mode": php.WorkerMode,
		"workers":     php.Workers,
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, "序列化配置失败: "+err.Error(), http.StatusInternalServerError)
	}
}

// handlePHPConfigSave 保存 PHP 模块配置
func (h *ConfigHandler) handlePHPConfigSave(w http.ResponseWriter, r *http.Request) {
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

	var phpCfg map[string]interface{}
	if err := json.Unmarshal(body, &phpCfg); err != nil {
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
			phpFound := false
			for i := 0; i < len(doc.Content); i += 2 {
				keyNode := doc.Content[i]
				if keyNode.Kind == yaml.ScalarNode && keyNode.Value == "php" {
					newPHPNode := buildPHPNode(phpCfg)
					doc.Content[i+1] = newPHPNode
					phpFound = true
					break
				}
			}
			if !phpFound {
				newPHPNode := buildPHPNode(phpCfg)
				doc.Content = append(doc.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "php"},
					newPHPNode)
			}
		}
	}

	saveAndResponse(w, configPath, data, &fullNode)
}

// buildPHPNode 根据前端提交的配置构建 php YAML 节点
func buildPHPNode(phpCfg map[string]interface{}) *yaml.Node {
	node := &yaml.Node{Kind: yaml.MappingNode}

	// enabled (bool)
	if enabled, ok := phpCfg["enabled"]; ok {
		valStr := "false"
		if e, ok := enabled.(bool); ok && e {
			valStr = "true"
		}
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "enabled"},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: valStr})
	}

	// path (string)
	if path, ok := phpCfg["path"]; ok {
		pathStr := strings.TrimSpace(fmt.Sprintf("%v", path))
		if pathStr != "" {
			node.Content = append(node.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "path"},
				&yaml.Node{Kind: yaml.ScalarNode, Value: pathStr})
		}
	}

	// docroot (string)
	if docroot, ok := phpCfg["docroot"]; ok {
		docrootStr := strings.TrimSpace(fmt.Sprintf("%v", docroot))
		if docrootStr != "" {
			node.Content = append(node.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "docroot"},
				&yaml.Node{Kind: yaml.ScalarNode, Value: docrootStr})
		}
	}

	// index (list)
	if index, ok := phpCfg["index"]; ok {
		if indexList, ok := index.([]interface{}); ok && len(indexList) > 0 {
			seqNode := &yaml.Node{Kind: yaml.SequenceNode}
			for _, idx := range indexList {
				idxStr := strings.TrimSpace(fmt.Sprintf("%v", idx))
				if idxStr != "" {
					seqNode.Content = append(seqNode.Content,
						&yaml.Node{Kind: yaml.ScalarNode, Value: idxStr})
				}
			}
			if len(seqNode.Content) > 0 {
				node.Content = append(node.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "index"},
					seqNode)
			}
		}
	}

	// worker_mode (bool)
	if wm, ok := phpCfg["worker_mode"]; ok {
		valStr := "false"
		if e, ok := wm.(bool); ok && e {
			valStr = "true"
		}
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "worker_mode"},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: valStr})
	}

	// workers (int)
	if workers, ok := phpCfg["workers"]; ok {
		workersStr := fmt.Sprintf("%v", workers)
		if workersStr != "" && workersStr != "0" {
			node.Content = append(node.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "workers"},
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: workersStr})
		}
	}

	return node
}
