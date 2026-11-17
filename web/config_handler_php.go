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

// handlePHPConfig 获取当前 PHP 模块配置
func (h *ConfigHandler) handlePHPConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	config.CfgMu.RLock()
	php := config.Cfg.PHP
	config.CfgMu.RUnlock()

	// 未配置 index 时为 nil，JSON 序列化成 null 会让前端 .map 崩溃；统一回空数组
	index := php.Index
	if index == nil {
		index = []string{}
	}
	allowedRoots := php.AllowedRoots
	if allowedRoots == nil {
		allowedRoots = []string{}
	}

	// docroot 安全性提示：配置被手工改成 / 或系统目录时，PHP 模块与代码管理页
	// 会拒绝服务，这里把原因带给前端，便于定位。
	docRootErr := ""
	if err := config.ValidateDocRoot(php.DocRoot); err != nil {
		docRootErr = err.Error()
	}

	resp := map[string]interface{}{
		"enabled":       php.Enabled,
		"path":          php.Path,
		"docroot":       php.DocRoot,
		"index":         index,
		"allowed_roots": allowedRoots,
		"worker_mode":   php.WorkerMode,
		"workers":       php.Workers,
		"docroot_error": docRootErr,
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

	// 部分更新保护：请求体未包含的字段沿用当前配置。
	// 否则只提交 docroot 时，enabled/path/index/workers/allowed_roots 会被整段清空，
	// PHP 模块与代码管理随即停摆（实测踩过：一次 docroot 校验请求把模块关了）。
	config.CfgMu.RLock()
	cur := config.Cfg.PHP
	config.CfgMu.RUnlock()
	strsToIface := func(in []string) []interface{} {
		out := make([]interface{}, len(in))
		for i, s := range in {
			out[i] = s
		}
		return out
	}
	if _, ok := phpCfg["enabled"]; !ok {
		phpCfg["enabled"] = cur.Enabled
	}
	if _, ok := phpCfg["path"]; !ok {
		phpCfg["path"] = cur.Path
	}
	if _, ok := phpCfg["docroot"]; !ok {
		phpCfg["docroot"] = cur.DocRoot
	}
	if _, ok := phpCfg["index"]; !ok {
		phpCfg["index"] = strsToIface(cur.Index)
	}
	if _, ok := phpCfg["allowed_roots"]; !ok {
		phpCfg["allowed_roots"] = strsToIface(cur.AllowedRoots)
	}
	if _, ok := phpCfg["worker_mode"]; !ok {
		phpCfg["worker_mode"] = cur.WorkerMode
	}
	if _, ok := phpCfg["workers"]; !ok {
		phpCfg["workers"] = cur.Workers
	}

	// 保存前校验 docroot：禁止磁盘根目录与系统/敏感目录（含通过软链接指向的情况），
	// 避免把 docroot 设成 / 后，PHP 脚本与代码管理页可以读写/删除任意系统文件。
	if raw, ok := phpCfg["docroot"]; ok {
		docrootStr := strings.TrimSpace(fmt.Sprintf("%v", raw))
		if docrootStr == "" {
			docrootStr = "www"
		}
		if err := config.ValidateDocRoot(docrootStr); err != nil {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "error", "message": "docroot 不安全：" + err.Error()})
			return
		}
	}

	// 额外代码目录（多目录）同样做安全校验，禁止加入根目录/系统敏感目录
	if raw, ok := phpCfg["allowed_roots"]; ok {
		if list, ok := raw.([]interface{}); ok {
			for _, item := range list {
				p := strings.TrimSpace(fmt.Sprintf("%v", item))
				if p == "" {
					continue
				}
				if err := config.ValidateDocRoot(p); err != nil {
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					w.WriteHeader(http.StatusBadRequest)
					_ = json.NewEncoder(w).Encode(map[string]string{
						"status":  "error",
						"message": fmt.Sprintf("allowed_roots 项不安全（%s）：%v", p, err),
					})
					return
				}
			}
		}
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

	saveAndResponse(w, configPath, &fullNode)
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

	// allowed_roots (list)：额外代码目录（多目录）
	if raw, ok := phpCfg["allowed_roots"]; ok {
		if list, ok := raw.([]interface{}); ok {
			seqNode := &yaml.Node{Kind: yaml.SequenceNode}
			for _, item := range list {
				p := strings.TrimSpace(fmt.Sprintf("%v", item))
				if p != "" {
					seqNode.Content = append(seqNode.Content,
						&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: p})
				}
			}
			if len(seqNode.Content) > 0 {
				node.Content = append(node.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "allowed_roots"},
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
