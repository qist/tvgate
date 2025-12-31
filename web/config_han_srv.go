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

// handleServerEditor 处理服务器配置编辑器页面
func (h *ConfigHandler) handleServerEditor(w http.ResponseWriter, r *http.Request) {
	webPath := h.getWebPath()

	data := map[string]interface{}{
		"title":   "TVGate 服务器配置编辑器",
		"webPath": webPath,
	}

	if err := h.renderTemplate(w, r, "server_editor", "templates/server_editor.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleServerConfig 处理服务器配置获取请求
func (h *ConfigHandler) handleServerConfig(w http.ResponseWriter, r *http.Request) {
	// 设置响应头
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	// 获取当前配置
	config.CfgMu.RLock()
	server := config.Cfg.Server
	config.CfgMu.RUnlock()

	// 转换为可JSON序列化的格式
	serverConfig := map[string]interface{}{
		"port":              server.Port,
		"http_port":         server.HTTPPort,
		"certfile":          server.CertFile,
		"keyfile":           server.KeyFile,
		"ssl_protocols":     server.SSLProtocols,
		"ssl_ciphers":       server.SSLCiphers,
		"ssl_ecdh_curve":    server.SSLECDHCurve,
		"http_to_https":     server.HTTPToHTTPS,
		"multicast_ifaces":  server.MulticastIfaces,
		"mcast_rejoin_interval": server.McastRejoinInterval.String(),
		"fcc_type":          server.FccType,
		"fcc_cache_size":    server.FccCacheSize,
		"fcc_listen_port_min": server.FccListenPortMin,
		"fcc_listen_port_max": server.FccListenPortMax,
		"upstream_interface": server.UpstreamInterface,
		"upstream_interface_fcc":  server.UpstreamInterfaceFcc,
		"tls": map[string]interface{}{
			"https_port":    server.TLS.HTTPSPort,
			"certfile":      server.TLS.CertFile,
			"keyfile":       server.TLS.KeyFile,
			"ssl_protocols": server.TLS.Protocols,
			"ssl_ciphers":   server.TLS.Ciphers,
			"ssl_ecdh_curve": server.TLS.ECDHCurve,
			"enable_h3":     server.TLS.EnableH3,
		},
		"ts": map[string]interface{}{
			"cache_size": server.TS.CacheSize,
			"cache_ttl":  server.TS.CacheTTL.String(),
		},
	}

	// 返回JSON格式的配置
	if err := json.NewEncoder(w).Encode(serverConfig); err != nil {
		http.Error(w, "序列化配置失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
}

// handleServerConfigSave 处理服务器配置保存请求
func (h *ConfigHandler) handleServerConfigSave(w http.ResponseWriter, r *http.Request) {
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
	var serverConfig map[string]interface{}
	if err := json.Unmarshal(body, &serverConfig); err != nil {
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

	// 查找并更新server节点
	if fullNode.Kind == yaml.DocumentNode && len(fullNode.Content) > 0 {
		doc := fullNode.Content[0]
		if doc.Kind == yaml.MappingNode {
			// 查找server节点
			serverFound := false
			for i := 0; i < len(doc.Content); i += 2 {
				keyNode := doc.Content[i]
				if keyNode.Kind == yaml.ScalarNode && keyNode.Value == "server" {
					// 创建新的server节点
					newServerNode := &yaml.Node{Kind: yaml.MappingNode}

					// 添加port
					if port, ok := serverConfig["port"]; ok {
						newServerNode.Content = append(newServerNode.Content,
							&yaml.Node{Kind: yaml.ScalarNode, Value: "port"},
							&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", port)})
					}

					// 添加http_port
					if httpPort, ok := serverConfig["http_port"]; ok {
						httpPortValue := fmt.Sprintf("%v", httpPort)
						if httpPortValue != "" && httpPortValue != "0" {
							newServerNode.Content = append(newServerNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "http_port"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: httpPortValue})
						}
					}

					// 添加certfile
					if certfile, ok := serverConfig["certfile"]; ok {
						certfileStr := fmt.Sprintf("%v", certfile)
						if certfileStr != "" {
							newServerNode.Content = append(newServerNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "certfile"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: certfileStr, Style: yaml.DoubleQuotedStyle})
						}
					}

					// 添加keyfile
					if keyfile, ok := serverConfig["keyfile"]; ok {
						keyfileStr := fmt.Sprintf("%v", keyfile)
						if keyfileStr != "" {
							newServerNode.Content = append(newServerNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "keyfile"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: keyfileStr, Style: yaml.DoubleQuotedStyle})
						}
					}

					// 添加ssl_protocols
					if sslProtocols, ok := serverConfig["ssl_protocols"]; ok {
						sslProtocolsStr := fmt.Sprintf("%v", sslProtocols)
						if sslProtocolsStr != "" {
							// 去除可能存在的引号
							// trimmed := strings.Trim(sslProtocolsStr, "\"")
							newServerNode.Content = append(newServerNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "ssl_protocols"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: sslProtocolsStr, Style: yaml.DoubleQuotedStyle})
						}
					}

					// 添加ssl_ciphers
					if sslCiphers, ok := serverConfig["ssl_ciphers"]; ok {
						sslCiphersStr := fmt.Sprintf("%v", sslCiphers)
						if sslCiphersStr != "" {
							// 去除可能存在的引号
							// trimmed := strings.Trim(sslCiphersStr, "\"")
							newServerNode.Content = append(newServerNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "ssl_ciphers"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: sslCiphersStr, Style: yaml.DoubleQuotedStyle})
						}
					}

					// 添加ssl_ecdh_curve
					if sslECDHCurve, ok := serverConfig["ssl_ecdh_curve"]; ok {
						sslECDHCurveStr := fmt.Sprintf("%v", sslECDHCurve)
						if sslECDHCurveStr != "" {
							// 去除可能存在的引号
							// trimmed := strings.Trim(sslECDHCurveStr, "\"")
							newServerNode.Content = append(newServerNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "ssl_ecdh_curve"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: sslECDHCurveStr, Style: yaml.DoubleQuotedStyle})
						}
					}

					// 添加http_to_https
					if httpToHTTPS, ok := serverConfig["http_to_https"]; ok {
						httpToHTTPSStr := fmt.Sprintf("%v", httpToHTTPS)
						if httpToHTTPSStr == "true" || httpToHTTPSStr == "1" {
							newServerNode.Content = append(newServerNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "http_to_https"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: "true"})
						}
					}

					// 添加multicast_ifaces
					if multicastIfaces, ok := serverConfig["multicast_ifaces"]; ok {
						if ifaces, ok := multicastIfaces.([]interface{}); ok && len(ifaces) > 0 {
							ifacesNode := &yaml.Node{Kind: yaml.SequenceNode}
							for _, iface := range ifaces {
								ifaceStr := fmt.Sprintf("%v", iface)
								if ifaceStr != "" {
									ifacesNode.Content = append(ifacesNode.Content,
										&yaml.Node{Kind: yaml.ScalarNode, Value: ifaceStr})
								}
							}
							if len(ifacesNode.Content) > 0 {
								newServerNode.Content = append(newServerNode.Content,
									&yaml.Node{Kind: yaml.ScalarNode, Value: "multicast_ifaces"},
									ifacesNode)
							}
						} else if ifacesStr, ok := multicastIfaces.(string); ok {
							// 处理字符串形式的接口列表
							ifaces := strings.Split(ifacesStr, ",")
							ifacesNode := &yaml.Node{Kind: yaml.SequenceNode}
							for _, iface := range ifaces {
								iface = strings.TrimSpace(iface)
								if iface != "" {
									ifacesNode.Content = append(ifacesNode.Content,
										&yaml.Node{Kind: yaml.ScalarNode, Value: iface})
								}
							}
							if len(ifacesNode.Content) > 0 {
								newServerNode.Content = append(newServerNode.Content,
									&yaml.Node{Kind: yaml.ScalarNode, Value: "multicast_ifaces"},
									ifacesNode)
							}
						}
					}

					// 添加 mcast_rejoin_interval
					if mcastRejoinInterval, ok := serverConfig["mcast_rejoin_interval"]; ok {
						mcastRejoinIntervalStr := fmt.Sprintf("%v", mcastRejoinInterval)
						// 清理字符串，去除引号等可能的额外字符
						mcastRejoinIntervalStr = strings.Trim(mcastRejoinIntervalStr, "\"")
						if mcastRejoinIntervalStr != "" && mcastRejoinIntervalStr != "0s" && mcastRejoinIntervalStr != "0" {
							newServerNode.Content = append(newServerNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "mcast_rejoin_interval"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: mcastRejoinIntervalStr})
						}
					}

					// 添加fcc_type
					if fccType, ok := serverConfig["fcc_type"]; ok {
						fccTypeStr := fmt.Sprintf("%v", fccType)
						if fccTypeStr != "" {
							newServerNode.Content = append(newServerNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "fcc_type"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: fccTypeStr})
						}
					}

					// 添加fcc_cache_size
					if fccCacheSize, ok := serverConfig["fcc_cache_size"]; ok {
						fccCacheSizeValue := fmt.Sprintf("%v", fccCacheSize)
						if fccCacheSizeValue != "" && fccCacheSizeValue != "0" {
							newServerNode.Content = append(newServerNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "fcc_cache_size"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: fccCacheSizeValue})
						}
					}

					// 添加fcc_listen_port_min
					if fccListenPortMin, ok := serverConfig["fcc_listen_port_min"]; ok {
						fccListenPortMinValue := fmt.Sprintf("%v", fccListenPortMin)
						if fccListenPortMinValue != "" && fccListenPortMinValue != "0" {
							newServerNode.Content = append(newServerNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "fcc_listen_port_min"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: fccListenPortMinValue})
						}
					}

					// 添加fcc_listen_port_max
					if fccListenPortMax, ok := serverConfig["fcc_listen_port_max"]; ok {
						fccListenPortMaxValue := fmt.Sprintf("%v", fccListenPortMax)
						if fccListenPortMaxValue != "" && fccListenPortMaxValue != "0" {
							newServerNode.Content = append(newServerNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "fcc_listen_port_max"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: fccListenPortMaxValue})
						}
					}

					// 添加upstream_interface
					if upstreamInterface, ok := serverConfig["upstream_interface"]; ok {
						upstreamInterfaceStr := fmt.Sprintf("%v", upstreamInterface)
						if upstreamInterfaceStr != "" {
							newServerNode.Content = append(newServerNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "upstream_interface"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: upstreamInterfaceStr})
						}
					}
					
					// 添加upstream_interface_fcc
					if upstreamInterfaceFcc, ok := serverConfig["upstream_interface_fcc"]; ok {
						upstreamInterfaceFccStr := fmt.Sprintf("%v", upstreamInterfaceFcc)
						if upstreamInterfaceFccStr != "" {
							newServerNode.Content = append(newServerNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "upstream_interface_fcc"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: upstreamInterfaceFccStr})
						}
					}
					
					// 添加tls配置块
					if tlsConfig, ok := serverConfig["tls"]; ok {
						if tlsMap, ok := tlsConfig.(map[string]interface{}); ok {
							tlsNode := &yaml.Node{Kind: yaml.MappingNode}
							
							// 添加https_port
							if httpsPort, ok := tlsMap["https_port"]; ok {
								httpsPortValue := fmt.Sprintf("%v", httpsPort)
								if httpsPortValue != "" && httpsPortValue != "0" {
									tlsNode.Content = append(tlsNode.Content,
										&yaml.Node{Kind: yaml.ScalarNode, Value: "https_port"},
										&yaml.Node{Kind: yaml.ScalarNode, Value: httpsPortValue})
								}
							}
							
							// 添加certfile
							if certfile, ok := tlsMap["certfile"]; ok {
								certfileStr := fmt.Sprintf("%v", certfile)
								if certfileStr != "" {
									tlsNode.Content = append(tlsNode.Content,
										&yaml.Node{Kind: yaml.ScalarNode, Value: "certfile"},
										&yaml.Node{Kind: yaml.ScalarNode, Value: certfileStr, Style: yaml.DoubleQuotedStyle})
								}
							}
							
							// 添加keyfile
							if keyfile, ok := tlsMap["keyfile"]; ok {
								keyfileStr := fmt.Sprintf("%v", keyfile)
								if keyfileStr != "" {
									tlsNode.Content = append(tlsNode.Content,
										&yaml.Node{Kind: yaml.ScalarNode, Value: "keyfile"},
										&yaml.Node{Kind: yaml.ScalarNode, Value: keyfileStr, Style: yaml.DoubleQuotedStyle})
								}
							}
							
							// 添加ssl_protocols
							if sslProtocols, ok := tlsMap["ssl_protocols"]; ok {
								sslProtocolsStr := fmt.Sprintf("%v", sslProtocols)
								if sslProtocolsStr != "" {
									tlsNode.Content = append(tlsNode.Content,
										&yaml.Node{Kind: yaml.ScalarNode, Value: "ssl_protocols"},
										&yaml.Node{Kind: yaml.ScalarNode, Value: sslProtocolsStr, Style: yaml.DoubleQuotedStyle})
								}
							}
							
							// 添加ssl_ciphers
							if sslCiphers, ok := tlsMap["ssl_ciphers"]; ok {
								sslCiphersStr := fmt.Sprintf("%v", sslCiphers)
								if sslCiphersStr != "" {
									tlsNode.Content = append(tlsNode.Content,
										&yaml.Node{Kind: yaml.ScalarNode, Value: "ssl_ciphers"},
										&yaml.Node{Kind: yaml.ScalarNode, Value: sslCiphersStr, Style: yaml.DoubleQuotedStyle})
								}
							}
							
							// 添加ssl_ecdh_curve
							if sslECDHCurve, ok := tlsMap["ssl_ecdh_curve"]; ok {
								sslECDHCurveStr := fmt.Sprintf("%v", sslECDHCurve)
								if sslECDHCurveStr != "" {
									tlsNode.Content = append(tlsNode.Content,
										&yaml.Node{Kind: yaml.ScalarNode, Value: "ssl_ecdh_curve"},
										&yaml.Node{Kind: yaml.ScalarNode, Value: sslECDHCurveStr, Style: yaml.DoubleQuotedStyle})
								}
							}
							
							// 添加enable_h3
							if enableH3, ok := tlsMap["enable_h3"]; ok {
								enableH3Str := fmt.Sprintf("%v", enableH3)
								if enableH3Str == "true" || enableH3Str == "1" {
									tlsNode.Content = append(tlsNode.Content,
										&yaml.Node{Kind: yaml.ScalarNode, Value: "enable_h3"},
										&yaml.Node{Kind: yaml.ScalarNode, Value: "true"})
								}
							}
							
							if len(tlsNode.Content) > 0 {
								newServerNode.Content = append(newServerNode.Content,
									&yaml.Node{Kind: yaml.ScalarNode, Value: "tls"},
									tlsNode)
							}
						}
					}

					// 添加ts配置块
					if tsConfig, ok := serverConfig["ts"]; ok {
						if tsMap, ok := tsConfig.(map[string]interface{}); ok {
							tsNode := &yaml.Node{Kind: yaml.MappingNode}
							
							// 添加cache_size
							if cacheSize, ok := tsMap["cache_size"]; ok {
								cacheSizeValue := fmt.Sprintf("%v", cacheSize)
								if cacheSizeValue != "" && cacheSizeValue != "0" {
									tsNode.Content = append(tsNode.Content,
										&yaml.Node{Kind: yaml.ScalarNode, Value: "cache_size"},
										&yaml.Node{Kind: yaml.ScalarNode, Value: cacheSizeValue})
								}
							}
							
							// 添加cache_ttl
							if cacheTTL, ok := tsMap["cache_ttl"]; ok {
								cacheTTLValue := fmt.Sprintf("%v", cacheTTL)
								if cacheTTLValue != "" && cacheTTLValue != "0s" && cacheTTLValue != "0" {
									tsNode.Content = append(tsNode.Content,
										&yaml.Node{Kind: yaml.ScalarNode, Value: "cache_ttl"},
										&yaml.Node{Kind: yaml.ScalarNode, Value: cacheTTLValue})
								}
							}
							
							if len(tsNode.Content) > 0 {
								newServerNode.Content = append(newServerNode.Content,
									&yaml.Node{Kind: yaml.ScalarNode, Value: "ts"},
									tsNode)
							}
						}
					}

					// 替换server节点
					doc.Content[i+1] = newServerNode
					serverFound = true
					break
				}
			}

			// 如果没有找到server节点，则创建一个新的
			if !serverFound {
				doc.Content = append(doc.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "server"},
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
