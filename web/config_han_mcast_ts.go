package web

import (
	"encoding/json"
	"fmt"
	"github.com/qist/tvgate/config/load"
	"github.com/qist/tvgate/logger"
	"io"
	"net/http"
	"os"

	"github.com/qist/tvgate/config"
	"gopkg.in/yaml.v3"
)

// handleMulticastConfig 处理组播配置获取请求
func (h *ConfigHandler) handleMulticastConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	config.CfgMu.RLock()
	mcast := config.Cfg.Multicast
	config.CfgMu.RUnlock()

	// 未配置组播段时 MulticastIfaces 为 nil，JSON 序列化成 null 会让前端 .map 崩溃；统一回空数组
	ifaces := mcast.MulticastIfaces
	if ifaces == nil {
		ifaces = []string{}
	}

	mcastConfig := map[string]interface{}{
		"multicast_ifaces":       ifaces,
		"mcast_rejoin_interval":  mcast.McastRejoinInterval.String(),
		"fcc_type":               mcast.FccType,
		"fcc_cache_size":         mcast.FccCacheSize,
		"fcc_listen_port_min":    mcast.FccListenPortMin,
		"fcc_listen_port_max":    mcast.FccListenPortMax,
		"upstream_interface":     mcast.UpstreamInterface,
		"upstream_interface_fcc": mcast.UpstreamInterfaceFcc,
		// 传递默认值，避免前端硬编码
		"defaults": map[string]interface{}{
			"fcc_cache_size":      16384,
			"fcc_listen_port_min": 40000,
			"fcc_listen_port_max": 50000,
		},
	}

	if err := json.NewEncoder(w).Encode(mcastConfig); err != nil {
		http.Error(w, "序列化配置失败: "+err.Error(), http.StatusInternalServerError)
	}
}

// handleTSConfig 处理TS缓存配置获取请求
func (h *ConfigHandler) handleTSConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	config.CfgMu.RLock()
	ts := config.Cfg.TS
	config.CfgMu.RUnlock()

	tsConfig := map[string]interface{}{
		"enable":     ts.Enable,
		"cache_size": ts.CacheSize,
		"cache_ttl":  ts.CacheTTL.String(),
	}

	if err := json.NewEncoder(w).Encode(tsConfig); err != nil {
		http.Error(w, "序列化配置失败: "+err.Error(), http.StatusInternalServerError)
	}
}

// handleMulticastConfigSave 处理组播配置保存请求
func (h *ConfigHandler) handleMulticastConfigSave(w http.ResponseWriter, r *http.Request) {
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

	var mcastConfig map[string]interface{}
	if err := json.Unmarshal(body, &mcastConfig); err != nil {
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
			mcastFound := false
			for i := 0; i < len(doc.Content); i += 2 {
				keyNode := doc.Content[i]
				if keyNode.Kind == yaml.ScalarNode && keyNode.Value == "multicast" {
					newMcastNode := &yaml.Node{Kind: yaml.MappingNode}

					// 添加multicast_ifaces
					if ifaces, ok := mcastConfig["multicast_ifaces"]; ok {
						if ifaceList, ok := ifaces.([]interface{}); ok {
							seqNode := &yaml.Node{Kind: yaml.SequenceNode}
							for _, iface := range ifaceList {
								ifaceStr := fmt.Sprintf("%v", iface)
								if ifaceStr != "" {
									seqNode.Content = append(seqNode.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: ifaceStr})
								}
							}
							newMcastNode.Content = append(newMcastNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "multicast_ifaces"},
								seqNode)
						}
					}

					// 添加其他字段
					fields := []string{"mcast_rejoin_interval", "fcc_type", "fcc_cache_size", "fcc_listen_port_min", "fcc_listen_port_max", "upstream_interface", "upstream_interface_fcc"}
					for _, field := range fields {
						if val, ok := mcastConfig[field]; ok {
							valStr := fmt.Sprintf("%v", val)
							if valStr != "" && valStr != "0" {
								newMcastNode.Content = append(newMcastNode.Content,
									&yaml.Node{Kind: yaml.ScalarNode, Value: field},
									&yaml.Node{Kind: yaml.ScalarNode, Value: valStr})
							}
						}
					}

					doc.Content[i+1] = newMcastNode
					mcastFound = true
					break
				}
			}

			if !mcastFound {
				newMcastNode := &yaml.Node{Kind: yaml.MappingNode}
				// ... same logic for adding fields ...
				// (simplifying for now, just append it)
				doc.Content = append(doc.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "multicast"},
					newMcastNode)

				// Re-run the field addition for the new node
				fields := []string{"mcast_rejoin_interval", "fcc_type", "fcc_cache_size", "fcc_listen_port_min", "fcc_listen_port_max", "upstream_interface", "upstream_interface_fcc"}
				if ifaces, ok := mcastConfig["multicast_ifaces"]; ok {
					if ifaceList, ok := ifaces.([]interface{}); ok {
						seqNode := &yaml.Node{Kind: yaml.SequenceNode}
						for _, iface := range ifaceList {
							ifaceStr := fmt.Sprintf("%v", iface)
							if ifaceStr != "" {
								seqNode.Content = append(seqNode.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: ifaceStr})
							}
						}
						newMcastNode.Content = append(newMcastNode.Content,
							&yaml.Node{Kind: yaml.ScalarNode, Value: "multicast_ifaces"},
							seqNode)
					}
				}
				for _, field := range fields {
					if val, ok := mcastConfig[field]; ok {
						valStr := fmt.Sprintf("%v", val)
						if valStr != "" && valStr != "0" {
							newMcastNode.Content = append(newMcastNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: field},
								&yaml.Node{Kind: yaml.ScalarNode, Value: valStr})
						}
					}
				}
			}
		}
	}

	saveAndResponse(w, configPath, &fullNode)
}

// handleTSConfigSave 处理TS缓存配置保存请求
func (h *ConfigHandler) handleTSConfigSave(w http.ResponseWriter, r *http.Request) {
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

	var tsConfig map[string]interface{}
	if err := json.Unmarshal(body, &tsConfig); err != nil {
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
			tsFound := false
			for i := 0; i < len(doc.Content); i += 2 {
				keyNode := doc.Content[i]
				if keyNode.Kind == yaml.ScalarNode && keyNode.Value == "ts" {
					newTSNode := &yaml.Node{Kind: yaml.MappingNode}

					if enable, ok := tsConfig["enable"]; ok {
						enableStr := "false"
						if e, ok := enable.(bool); ok && e {
							enableStr = "true"
						}
						newTSNode.Content = append(newTSNode.Content,
							&yaml.Node{Kind: yaml.ScalarNode, Value: "enable"},
							&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: enableStr})
					}

					fields := []string{"cache_size", "cache_ttl"}
					for _, field := range fields {
						if val, ok := tsConfig[field]; ok {
							valStr := fmt.Sprintf("%v", val)
							if valStr != "" && valStr != "0" && valStr != "0s" {
								newTSNode.Content = append(newTSNode.Content,
									&yaml.Node{Kind: yaml.ScalarNode, Value: field},
									&yaml.Node{Kind: yaml.ScalarNode, Value: valStr})
							}
						}
					}

					doc.Content[i+1] = newTSNode
					tsFound = true
					break
				}
			}

			if !tsFound {
				newTSNode := &yaml.Node{Kind: yaml.MappingNode}
				doc.Content = append(doc.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "ts"},
					newTSNode)

				if enable, ok := tsConfig["enable"]; ok {
					enableStr := "false"
					if e, ok := enable.(bool); ok && e {
						enableStr = "true"
					}
					newTSNode.Content = append(newTSNode.Content,
						&yaml.Node{Kind: yaml.ScalarNode, Value: "enable"},
						&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: enableStr})
				}

				fields := []string{"cache_size", "cache_ttl"}
				for _, field := range fields {
					if val, ok := tsConfig[field]; ok {
						valStr := fmt.Sprintf("%v", val)
						if valStr != "" && valStr != "0" && valStr != "0s" {
							newTSNode.Content = append(newTSNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: field},
								&yaml.Node{Kind: yaml.ScalarNode, Value: valStr})
						}
					}
				}
			}
		}
	}

	saveAndResponse(w, configPath, &fullNode)
}

// saveAndResponse 辅助函数，保存配置并返回响应
func saveAndResponse(w http.ResponseWriter, configPath string, fullNode *yaml.Node) {
	newData, err := yaml.Marshal(fullNode)
	if err != nil {
		http.Error(w, "序列化配置失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 备份旧配置（内容无变化或已有同态快照时自动跳过）
	if _, err := backupConfigFile(configPath, newData); err != nil {
		http.Error(w, "创建备份文件失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err := os.WriteFile(configPath, newData, 0644); err != nil {
		http.Error(w, "写入配置文件失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 立即重载内存配置：各 GET API 读 config.Cfg，磁盘变更要等 fsnotify
	// 防抖（reload: 5 秒）后才重新加载——保存后不等防抖，前端立即读到新值
	if err := load.LoadConfig(configPath); err != nil {
		logger.LogPrintf("保存后重载配置失败: %v", err)
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("配置保存成功"))
}
