package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/config/load"
	"github.com/qist/tvgate/publisher"
	"gopkg.in/yaml.v3"
)

func (h *ConfigHandler) handlePublisherEditor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}

	webPath := h.getWebPath()

	data := map[string]interface{}{
		"title":   "TVGate 推流管理",
		"webPath": webPath,
	}

	if err := h.renderTemplate(w, r, "publisher_editor", "templates/publisher_editor.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func (h *ConfigHandler) handlePublisherStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}

	type streamStatus struct {
		Name       string                        `json:"name"`
		Enabled    bool                          `json:"enabled"`
		Protocol   string                        `json:"protocol,omitempty"`
		Primary    *publisher.FFmpegProcessStats `json:"primary,omitempty"`
		HasManager bool                          `json:"has_manager"`
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	config.CfgMu.RLock()
	pcfg := config.Cfg.Publisher
	config.CfgMu.RUnlock()

	if pcfg == nil || len(pcfg.Streams) == 0 {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"streams": []streamStatus{},
		})
		return
	}

	manager := publisher.GetManager()

	streams := make([]streamStatus, 0, len(pcfg.Streams))
	for name, item := range pcfg.Streams {
		st := streamStatus{
			Name:       name,
			Enabled:    item != nil && item.Enabled,
			HasManager: manager != nil,
		}
		if item != nil {
			st.Protocol = item.Protocol
		}
		if manager != nil {
			st.Primary = manager.GetFFmpegStats(name, 1)
		}
		streams = append(streams, st)
	}

	sort.Slice(streams, func(i, j int) bool { return streams[i].Name < streams[j].Name })

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"streams": streams,
		"ts":      time.Now().Unix(),
	})
}

func (h *ConfigHandler) handlePublisherFFmpegStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	ffPath, err := publisher.FindFFmpeg()
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"installed": false,
			"error":     err.Error(),
			"hint":      "请安装 ffmpeg，并确保在 ./ffmpeg/bin/ffmpeg 或系统 PATH 中可找到",
		})
		return
	}

	version := ""
	cmd := exec.Command(ffPath, "-version")
	out, vErr := cmd.Output()
	if vErr == nil {
		lines := strings.Split(string(out), "\n")
		if len(lines) > 0 {
			version = strings.TrimSpace(lines[0])
		}
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"installed": true,
		"path":      ffPath,
		"version":   version,
	})
}

func (h *ConfigHandler) handlePublisherConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	config.CfgMu.RLock()
	publisherCfg := config.Cfg.Publisher
	config.CfgMu.RUnlock()

	if publisherCfg == nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{})
		return
	}

	yamlData, err := yaml.Marshal(publisherCfg)
	if err != nil {
		http.Error(w, "序列化配置失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var publisherMap map[string]interface{}
	if err := yaml.Unmarshal(yamlData, &publisherMap); err != nil {
		http.Error(w, "解析配置失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err := json.NewEncoder(w).Encode(publisherMap); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func (h *ConfigHandler) handlePublisherConfigSave(w http.ResponseWriter, r *http.Request) {
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

	var publisherMap map[string]interface{}
	if err := json.Unmarshal(body, &publisherMap); err != nil {
		http.Error(w, "解析JSON失败: "+err.Error(), http.StatusBadRequest)
		return
	}

	configPath := *config.ConfigFilePath
	oldData, err := os.ReadFile(configPath)
	if err != nil {
		http.Error(w, "读取配置文件失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var fullNode yaml.Node
	if err := yaml.Unmarshal(oldData, &fullNode); err != nil {
		http.Error(w, "解析配置文件失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	newPublisherNode := interfaceToYAMLNode(publisherMap, "path")
	if newPublisherNode.Kind != yaml.MappingNode {
		http.Error(w, "publisher 节点必须是对象", http.StatusBadRequest)
		return
	}

	if fullNode.Kind == yaml.DocumentNode && len(fullNode.Content) > 0 {
		doc := fullNode.Content[0]
		if doc.Kind == yaml.MappingNode {
			found := false
			for i := 0; i < len(doc.Content); i += 2 {
				keyNode := doc.Content[i]
				if keyNode.Kind == yaml.ScalarNode && keyNode.Value == "publisher" {
					doc.Content[i+1] = newPublisherNode
					found = true
					break
				}
			}

			if !found {
				doc.Content = append(doc.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "publisher"},
					newPublisherNode,
				)
			}
		}
	}

	newData, err := yaml.Marshal(&fullNode)
	if err != nil {
		http.Error(w, "序列化配置失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	backupPath := configPath + ".backup." + time.Now().Format("20060102150405")
	if err := os.WriteFile(backupPath, oldData, 0644); err != nil {
		http.Error(w, "创建备份文件失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err := os.WriteFile(configPath, newData, 0644); err != nil {
		_ = os.WriteFile(configPath, oldData, 0644)
		http.Error(w, "写入配置文件失败，已恢复备份: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 立即重载内存配置，确保新建/修改的推流可被前端读取
	if err := load.LoadConfig(configPath); err != nil {
		_ = os.WriteFile(configPath, oldData, 0644)
		http.Error(w, "重新加载配置失败，已恢复备份: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// 刷新 publisher manager（若已初始化）
	publisher.UpdatePublisherConfig()
	// 若之前未初始化，尝试启动 publisher（配置首次创建的场景）
	if publisher.GetManager() == nil {
		if err := publisher.Init(); err != nil {
			http.Error(w, "初始化 publisher 失败: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("配置保存成功"))
}

func interfaceToYAMLNode(v interface{}, firstKey string) *yaml.Node {
	switch val := v.(type) {
	case map[string]interface{}:
		node := &yaml.Node{Kind: yaml.MappingNode}

		keys := make([]string, 0, len(val))
		for k := range val {
			if k == firstKey {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if firstKey != "" {
			if _, ok := val[firstKey]; ok {
				keys = append([]string{firstKey}, keys...)
			}
		}

		for _, k := range keys {
			node.Content = append(node.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: k},
				interfaceToYAMLNode(val[k], ""),
			)
		}
		return node

	case map[interface{}]interface{}:
		m := make(map[string]interface{}, len(val))
		for k, v2 := range val {
			m[fmt.Sprintf("%v", k)] = v2
		}
		return interfaceToYAMLNode(m, firstKey)

	case []interface{}:
		seq := &yaml.Node{Kind: yaml.SequenceNode}
		for _, item := range val {
			seq.Content = append(seq.Content, interfaceToYAMLNode(item, ""))
		}
		return seq

	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: val}
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: strconv.FormatBool(val)}
	case float64:
		if val == float64(int64(val)) {
			return &yaml.Node{Kind: yaml.ScalarNode, Value: strconv.FormatInt(int64(val), 10)}
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Value: strconv.FormatFloat(val, 'f', -1, 64)}
	case float32:
		f := float64(val)
		if f == float64(int64(f)) {
			return &yaml.Node{Kind: yaml.ScalarNode, Value: strconv.FormatInt(int64(f), 10)}
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Value: strconv.FormatFloat(f, 'f', -1, 64)}
	case int:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: strconv.Itoa(val)}
	case int64:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: strconv.FormatInt(val, 10)}
	case uint64:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: strconv.FormatUint(val, 10)}
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
	default:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", v)}
	}
}
