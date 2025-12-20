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

// handleHTTPEditor 处理 HTTP 配置编辑器页面
func (h *ConfigHandler) handleHTTPEditor(w http.ResponseWriter, r *http.Request) {
	webPath := h.getWebPath()

	data := map[string]interface{}{
		"title":   "TVGate HTTP 配置编辑器",
		"webPath": webPath,
	}

	if err := h.renderTemplate(w, r, "http_editor", "templates/http_editor.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleHTTPConfig 获取当前 HTTP 配置
func (h *ConfigHandler) handleHTTPConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	config.CfgMu.RLock()
	httpCfg := config.Cfg.HTTP
	config.CfgMu.RUnlock()

	resp := map[string]interface{}{
		"timeout":                 httpCfg.Timeout.String(),
		"connect_timeout":         httpCfg.ConnectTimeout.String(),
		"keepalive":               httpCfg.KeepAlive.String(),
		"response_header_timeout": httpCfg.ResponseHeaderTimeout.String(),
		"idle_conn_timeout":       httpCfg.IdleConnTimeout.String(),
		"tls_handshake_timeout":   httpCfg.TLSHandshakeTimeout.String(),
		"expect_continue_timeout": httpCfg.ExpectContinueTimeout.String(),
		"max_idle_conns":          httpCfg.MaxIdleConns,
		"max_idle_conns_per_host": httpCfg.MaxIdleConnsPerHost,
		"max_conns_per_host":      httpCfg.MaxConnsPerHost,
		"disable_keepalives":      httpCfg.DisableKeepAlives,
		"insecure_skip_verify":    httpCfg.InsecureSkipVerify,
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, "序列化配置失败: "+err.Error(), http.StatusInternalServerError)
	}
}

// handleHTTPConfigSave 保存 HTTP 配置
func (h *ConfigHandler) handleHTTPConfigSave(w http.ResponseWriter, r *http.Request) {
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

	var httpConfig map[string]interface{}
	if err := json.Unmarshal(body, &httpConfig); err != nil {
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

	// 查找并更新 HTTP 节点
	if fullNode.Kind == yaml.DocumentNode && len(fullNode.Content) > 0 {
		doc := fullNode.Content[0]
		if doc.Kind == yaml.MappingNode {
			httpFound := false
			for i := 0; i < len(doc.Content); i += 2 {
				keyNode := doc.Content[i]
				if keyNode.Kind == yaml.ScalarNode && keyNode.Value == "http" {
					newHTTPNode := &yaml.Node{Kind: yaml.MappingNode}

					for k, v := range httpConfig {
						valueStr := fmt.Sprintf("%v", v)
						// 处理时间字符串保持格式
						if keyStr, ok := v.(string); ok && (k != "max_idle_conns" && k != "max_idle_conns_per_host" && k != "max_conns_per_host" && k != "disable_keepalives") {
							valueStr = formatDurationString(keyStr)
						}
						newHTTPNode.Content = append(newHTTPNode.Content,
							&yaml.Node{Kind: yaml.ScalarNode, Value: k},
							&yaml.Node{Kind: yaml.ScalarNode, Value: valueStr})
					}

					doc.Content[i+1] = newHTTPNode
					httpFound = true
					break
				}
			}

			// 没找到 http 节点就新建
			if !httpFound {
				newHTTPNode := &yaml.Node{Kind: yaml.MappingNode}
				doc.Content = append(doc.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "http"},
					newHTTPNode)
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
