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
	// "github.com/qist/tvgate/logger"
	"gopkg.in/yaml.v3"
)

// handleProxyGroupsEditor 处理代理组编辑器页面
func (h *ConfigHandler) handleProxyGroupsEditor(w http.ResponseWriter, r *http.Request) {
	webPath := h.getWebPath()
	// 获取监控路径，默认为/status
	monitorPath := config.Cfg.Monitor.Path
	if monitorPath == "" {
		monitorPath = "/status"
	} else {
		// 确保monitorPath以/开头
		if !strings.HasPrefix(monitorPath, "/") {
			monitorPath = "/" + monitorPath
		}
	}

	data := map[string]interface{}{
		"title":       "TVGate 代理组编辑器",
		"webPath":     webPath,
		"monitorPath": monitorPath,
	}

	if err := h.renderTemplate(w, r, "proxygroups_editor", "templates/proxygroups_editor.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleProxyGroupsConfig 处理代理组配置获取请求
func (h *ConfigHandler) handleProxyGroupsConfig(w http.ResponseWriter, r *http.Request) {
	// 设置响应头
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	// 获取当前配置
	config.CfgMu.RLock()
	proxyGroups := config.Cfg.ProxyGroups
	config.CfgMu.RUnlock()

	// 转换为可JSON序列化的格式
	proxyGroupsMap := make(map[string]interface{})

	for name, pg := range proxyGroups {
		proxies := make([]map[string]interface{}, len(pg.Proxies))
		for i, proxy := range pg.Proxies {
			// 打印单个代理日志
			// logger.LogPrintf("代理组 [%s] - 代理 #%d: %+v (类型: %T)", name, i, proxy, proxy)

			proxyMap := map[string]interface{}{
				"name":     proxy.Name,
				"type":     proxy.Type,
				"server":   proxy.Server,
				"port":     proxy.Port,
				"udp":      proxy.UDP,
				"username": proxy.Username,
				"password": proxy.Password,
			}

			// 添加 headers 配置（如果存在）
			if len(proxy.Headers) > 0 {
				proxyMap["headers"] = proxy.Headers
			}

			proxies[i] = proxyMap
		}

		// 复制代理组统计信息（如果存在）
		var statsMap map[string]interface{}
		if pg.Stats != nil {
			statsMap = make(map[string]interface{})
			proxyStatsMap := make(map[string]interface{})

			pg.Stats.RLock()
			for proxyName, proxyStats := range pg.Stats.ProxyStats {
				proxyStatsMap[proxyName] = map[string]interface{}{
					"LastCheck":     proxyStats.LastCheck,
					"LastUsed":      proxyStats.LastUsed,
					"ResponseTime":  proxyStats.ResponseTime,
					"Alive":         proxyStats.Alive,
					"FailCount":     proxyStats.FailCount,
					"CooldownUntil": proxyStats.CooldownUntil,
					"StatusCode":    proxyStats.StatusCode,
				}
			}
			pg.Stats.RUnlock()

			statsMap["ProxyStats"] = proxyStatsMap
		}

		pgMap := map[string]interface{}{
			"proxies":     proxies,
			"domains":     pg.Domains,
			"ipv6":        pg.IPv6,
			"interval":    formatDuration(pg.Interval),
			"loadbalance": pg.LoadBalance,
			"max_retries": pg.MaxRetries,
			"retry_delay": formatDuration(pg.RetryDelay),
			"max_rt":      formatDuration(pg.MaxRT),
			"stats":       statsMap,
		}

		// // 打印整个代理组 JSON 日志
		// if data, err := json.MarshalIndent(pgMap, "", "  "); err == nil {
		// 	logger.LogPrintf("代理组 [%s] 完整配置:\n%s", name, string(data))
		// } else {
		// 	logger.LogPrintf("代理组 [%s] 配置序列化失败: %v", name, err)
		// }

		proxyGroupsMap[name] = pgMap
	}

	// 返回 JSON 格式的配置
	if err := json.NewEncoder(w).Encode(proxyGroupsMap); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// handleProxyGroupsConfigSave 处理代理组配置保存请求
func (h *ConfigHandler) handleProxyGroupsConfigSave(w http.ResponseWriter, r *http.Request) {
	// 辅助函数：将接口值转换为 int
	interfaceToInt := func(v interface{}) (int, bool) {
		switch val := v.(type) {
		case float64:
			return int(val), true
		case int:
			return val, true
		case string:
			var i int
			if _, err := fmt.Sscanf(val, "%d", &i); err == nil {
				return i, true
			}
		}
		return 0, false
	}

	// 辅助函数：将接口值转换为 float64
	interfaceToFloat64 := func(v interface{}) (float64, bool) {
		switch val := v.(type) {
		case float64:
			return val, true
		case int:
			return float64(val), true
		case string:
			var f float64
			if _, err := fmt.Sscanf(val, "%f", &f); err == nil {
				return f, true
			}
		}
		return 0, false
	}

	// logger.LogPrintf("开始处理代理组配置保存请求")

	// 检查请求方法
	if r.Method != http.MethodPost {
		// logger.LogPrintf("错误：方法不允许: %s", r.Method)
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}

	// logger.LogPrintf("请求方法正确: %s", r.Method)

	// 读取请求体
	body, err := io.ReadAll(r.Body)
	if err != nil {
		// logger.LogPrintf("错误：读取请求体失败: %v", err)
		http.Error(w, "读取请求体失败: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// logger.LogPrintf("接收到的请求体数据: %s", string(body))

	// 解析JSON数据
	var proxyGroups map[string]map[string]interface{}
	if err := json.Unmarshal(body, &proxyGroups); err != nil {
		// logger.LogPrintf("错误：解析JSON失败: %v", err)
		http.Error(w, "解析JSON失败: "+err.Error(), http.StatusBadRequest)
		return
	}

	// logger.LogPrintf("解析后的代理组数据: %+v", proxyGroups)

	// 检查是否有数据
	// if len(proxyGroups) == 0 {
	// 	// logger.LogPrintf("警告：没有代理组数据")
	// }

	// 详细记录每个代理组的信息
	// for name, pg := range proxyGroups {
	// 	// logger.LogPrintf("代理组 [%s] 的详细信息: %+v", name, pg)
	// 	if proxies, ok := pg["proxies"]; ok {
	// 		// logger.LogPrintf("代理组 [%s] 的proxies字段: %+v (类型: %T)", name, proxies, proxies)
	// 		if proxiesList, ok := proxies.([]interface{}); ok {
	// 			// logger.LogPrintf("代理组 [%s] 的代理列表长度: %d", name, len(proxiesList))
	// 			for i, proxy := range proxiesList {
	// 				// logger.LogPrintf("代理 #%d: %+v (类型: %T)", i, proxy, proxy)
	// 			}
	// 		}
	// 	} else {
	// 		// logger.LogPrintf("代理组 [%s] 没有proxies字段", name)
	// 	}
	// }

	// 读取配置文件
	configPath := *config.ConfigFilePath
	// logger.LogPrintf("配置文件路径: %s", configPath)

	data, err := os.ReadFile(configPath)
	if err != nil {
		// logger.LogPrintf("错误：读取配置文件失败: %v", err)
		http.Error(w, "读取配置文件失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// logger.LogPrintf("成功读取配置文件: %s", configPath)

	// 使用yaml.Node解析YAML配置以保持注释和格式
	var fullNode yaml.Node
	if err := yaml.Unmarshal(data, &fullNode); err != nil {
		// logger.LogPrintf("错误：解析配置文件失败: %v", err)
		http.Error(w, "解析配置文件失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// logger.LogPrintf("成功解析YAML配置")

	// 转换代理组数据
	yamlProxyGroups := make(map[string]*yaml.Node)

	for name, pg := range proxyGroups {
		// logger.LogPrintf("处理代理组: %s", name)
		// logger.LogPrintf("代理组 %s 的原始数据: %+v", name, pg)
		proxyGroupNode := &yaml.Node{Kind: yaml.MappingNode}

		// 添加proxies字段（如果存在）
		if proxies, ok := pg["proxies"]; ok {
			// logger.LogPrintf("代理组 %s 的proxies字段类型: %T", name, proxies)
			if proxiesList, ok := proxies.([]interface{}); ok {
				// logger.LogPrintf("处理代理组 %s 的代理列表，共 %d 个代理", name, len(proxiesList))
				proxiesNode := &yaml.Node{Kind: yaml.SequenceNode}
				yamlProxies := make([]*yaml.Node, len(proxiesList))

				for i, p := range proxiesList {
					// logger.LogPrintf("处理代理 #%d: %v (类型: %T)", i, p, p)
					if proxyMap, ok := p.(map[string]interface{}); ok {
						proxyNode := &yaml.Node{Kind: yaml.MappingNode}

						// 添加基本字段
						if name, ok := proxyMap["name"]; ok && name != "" {
							proxyNode.Content = append(proxyNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "name"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", name)},
							)
							// logger.LogPrintf("添加代理名称: %s", name)
						}

						if typ, ok := proxyMap["type"]; ok && typ != "" {
							proxyNode.Content = append(proxyNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "type"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", typ)},
							)
							// logger.LogPrintf("添加代理类型: %s", typ)
						}

						if server, ok := proxyMap["server"]; ok && server != "" {
							proxyNode.Content = append(proxyNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "server"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", server)},
							)
							// logger.LogPrintf("添加服务器地址: %s", server)
						}

						// 处理端口，提供默认值 80
						port := 80
						if portVal, ok := proxyMap["port"]; ok {
							if p, ok := interfaceToInt(portVal); ok {
								port = p
							}
						}
						proxyNode.Content = append(proxyNode.Content,
							&yaml.Node{Kind: yaml.ScalarNode, Value: "port"},
							&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%d", port)},
						)
						// logger.LogPrintf("添加端口: %d", port)

						// 处理UDP字段，提供默认值 false
						udp := false
						if udpVal, ok := proxyMap["udp"]; ok {
							if udpBool, ok := udpVal.(bool); ok {
								udp = udpBool
							}
						}
						if udp {
							proxyNode.Content = append(proxyNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "udp"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", udp)},
							)
							// logger.LogPrintf("添加UDP设置: %v", udp)
						}

						// 添加用户名（如果存在且非空）
						if username, ok := proxyMap["username"]; ok && username != "" {
							proxyNode.Content = append(proxyNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "username"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", username)},
							)
							// logger.LogPrintf("添加用户名: %s", username)
						}

						// 添加密码（如果存在且非空）
						if password, ok := proxyMap["password"]; ok && password != "" {
							proxyNode.Content = append(proxyNode.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: "password"},
								&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", password)},
							)
							// logger.LogPrintf("添加密码: [HIDDEN]")
						}

						// 添加headers字段（如果存在）
						if headers, ok := proxyMap["headers"]; ok {
							// logger.LogPrintf("处理Headers: %v (类型: %T)", headers, headers)
							if headersMap, ok := headers.(map[string]interface{}); ok && len(headersMap) > 0 {
								headersNode := &yaml.Node{Kind: yaml.MappingNode}
								for k, v := range headersMap {
									headersNode.Content = append(headersNode.Content,
										&yaml.Node{Kind: yaml.ScalarNode, Value: k},
										&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", v)},
									)
								}
								proxyNode.Content = append(proxyNode.Content,
									&yaml.Node{Kind: yaml.ScalarNode, Value: "headers"},
									headersNode,
								)
								// logger.LogPrintf("添加Headers，共 %d 个", len(headersMap))
							}
						}

						// logger.LogPrintf("完成代理 #%d 的处理，节点内容数量: %d", i, len(proxyNode.Content))
						yamlProxies[i] = proxyNode
					}
				}

				// logger.LogPrintf("完成所有代理处理，代理数量: %d", len(yamlProxies))
				proxiesNode.Content = yamlProxies
				proxyGroupNode.Content = append(proxyGroupNode.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "proxies"},
					proxiesNode,
				)
				// logger.LogPrintf("已添加proxies字段到代理组 %s，当前节点内容数量: %d", name, len(proxyGroupNode.Content))
			} else {
				// logger.LogPrintf("代理组 %s 的proxies字段不是数组类型", name)
				// 添加空的proxies字段
				proxyGroupNode.Content = append(proxyGroupNode.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "proxies"},
					&yaml.Node{Kind: yaml.SequenceNode},
				)
				// logger.LogPrintf("代理组 %s proxies字段类型错误，添加空的proxies字段", name)
			}
		} else {
			// logger.LogPrintf("代理组 %s 没有proxies字段", name)
			// 添加空的proxies字段
			proxyGroupNode.Content = append(proxyGroupNode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "proxies"},
				&yaml.Node{Kind: yaml.SequenceNode},
			)
			// logger.LogPrintf("代理组 %s 缺少proxies字段，添加空的proxies字段", name)
		}

		// 添加domains字段（如果存在）
		if domains, ok := pg["domains"]; ok {
			if domainsList, ok := domains.([]interface{}); ok && len(domainsList) > 0 {
				// logger.LogPrintf("处理代理组 %s 的域名规则，共 %d 条", name, len(domainsList))
				domainsNode := &yaml.Node{Kind: yaml.SequenceNode}
				yamlDomains := make([]*yaml.Node, len(domainsList))

				for i, d := range domainsList {
					yamlDomains[i] = &yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", d)}
				}

				domainsNode.Content = yamlDomains
				proxyGroupNode.Content = append(proxyGroupNode.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "domains"},
					domainsNode,
				)
			}
		}

		// 添加ipv6字段，提供默认值 false
		ipv6 := false
		if ipv6Val, ok := pg["ipv6"]; ok {
			if ipv6Bool, ok := ipv6Val.(bool); ok {
				ipv6 = ipv6Bool
			}
		}
		if ipv6 {
			proxyGroupNode.Content = append(proxyGroupNode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "ipv6"},
				&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", ipv6)},
			)
			// logger.LogPrintf("添加IPv6设置: %v", ipv6)
		}

		// 添加interval字段，提供默认值 "180s"
		interval := "180s"
		if intervalVal, ok := pg["interval"]; ok && intervalVal != "" {
			if formattedInterval, err := formatDurationValue(intervalVal); err == nil {
				interval = formattedInterval
			}
		}
		proxyGroupNode.Content = append(proxyGroupNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "interval"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: interval},
		)
		// logger.LogPrintf("添加检查间隔: %s", interval)

		// 添加loadbalance字段，提供默认值 "round-robin"
		loadbalance := "round-robin"
		if loadbalanceVal, ok := pg["loadbalance"]; ok && loadbalanceVal != "" {
			loadbalance = fmt.Sprintf("%v", loadbalanceVal)
		}
		proxyGroupNode.Content = append(proxyGroupNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "loadbalance"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: loadbalance},
		)
		// logger.LogPrintf("添加负载均衡方式: %s", loadbalance)

		// 添加max_retries字段，提供默认值 1
		maxRetries := 1
		if maxRetriesVal, ok := pg["max_retries"]; ok {
			if maxRetriesFloat, ok := interfaceToFloat64(maxRetriesVal); ok {
				maxRetries = int(maxRetriesFloat)
			}
		}
		proxyGroupNode.Content = append(proxyGroupNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "max_retries"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%d", maxRetries)},
		)
		// logger.LogPrintf("添加最大重试次数: %d", maxRetries)

		// 添加retry_delay字段，提供默认值 "1s"
		retryDelay := "1s"
		if retryDelayVal, ok := pg["retry_delay"]; ok && retryDelayVal != "" {
			if formattedDelay, err := formatDurationValue(retryDelayVal); err == nil {
				retryDelay = formattedDelay
			}
		}
		proxyGroupNode.Content = append(proxyGroupNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "retry_delay"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: retryDelay},
		)
		// logger.LogPrintf("添加重试延迟: %s", retryDelay)

		// 添加max_rt字段，提供默认值 "200ms"
		maxRT := "200ms"
		if maxRTVal, ok := pg["max_rt"]; ok && maxRTVal != "" {
			if formattedRT, err := formatDurationValue(maxRTVal); err == nil {
				maxRT = formattedRT
			}
		}
		proxyGroupNode.Content = append(proxyGroupNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "max_rt"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: maxRT},
		)
		// logger.LogPrintf("添加最大响应时间: %s", maxRT)

		yamlProxyGroups[name] = proxyGroupNode
	}

	// 更新代理组配置，保持原有结构
	// logger.LogPrintf("开始更新代理组配置")
	if fullNode.Kind == yaml.DocumentNode && len(fullNode.Content) > 0 {
		doc := fullNode.Content[0]
		if doc.Kind == yaml.MappingNode {
			// 查找proxygroups节点并更新
			found := false
			for i := 0; i < len(doc.Content); i += 2 {
				keyNode := doc.Content[i]
				if keyNode.Kind == yaml.ScalarNode && keyNode.Value == "proxygroups" {
					proxyGroupsNode := doc.Content[i+1]
					if proxyGroupsNode.Kind != yaml.MappingNode {
						proxyGroupsNode.Kind = yaml.MappingNode
						proxyGroupsNode.Content = nil
					}

					// 将现有代理组建立索引，方便更新
					existingGroups := make(map[string]int)
					for j := 0; j < len(proxyGroupsNode.Content); j += 2 {
						existingGroups[proxyGroupsNode.Content[j].Value] = j
					}

					// 构建新的 Content 数组，仅包含传入的数据中存在的代理组
					var newContent []*yaml.Node
					// 我们需要保持原有的顺序，或者按传入的数据顺序重新构建
					// 这里选择按传入的数据顺序构建，这样删除操作自然就生效了
					for name, newNode := range yamlProxyGroups {
						newContent = append(newContent,
							&yaml.Node{Kind: yaml.ScalarNode, Value: name},
							newNode,
						)
					}
					proxyGroupsNode.Content = newContent
					found = true
					break
				}
			}

			// 如果没有找到proxygroups节点，则添加一个新的
			if !found {
				// logger.LogPrintf("未找到proxygroups节点，添加新的节点")
				doc.Content = append(doc.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "proxygroups"},
					&yaml.Node{Kind: yaml.MappingNode},
				)

				// 获取新添加的proxygroups节点并填充数据
				proxyGroupsNode := doc.Content[len(doc.Content)-1]
				for name, node := range yamlProxyGroups {
					proxyGroupsNode.Content = append(proxyGroupsNode.Content,
						&yaml.Node{Kind: yaml.ScalarNode, Value: name},
						node,
					)
				}
				// logger.LogPrintf("已添加新的proxygroups节点，包含 %d 个代理组", len(proxyGroupsNode.Content)/2)
			}
		} else {
			// logger.LogPrintf("文档根节点不是映射节点")
		}
	} else {
		// logger.LogPrintf("YAML文档结构不正确")
	}

	// 序列化为YAML格式
	// logger.LogPrintf("开始序列化YAML配置")
	newData, err := yaml.Marshal(&fullNode)
	if err != nil {
		// logger.LogPrintf("错误：序列化配置失败: %v", err)
		http.Error(w, "序列化配置失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// logger.LogPrintf("YAML序列化完成，数据长度: %d 字节", len(newData))

	// 创建备份文件
	backupPath := configPath + ".backup." + time.Now().Format("20060102150405")
	// logger.LogPrintf("创建备份文件: %s", backupPath)
	if err := os.WriteFile(backupPath, data, 0644); err != nil {
		// logger.LogPrintf("错误：创建备份文件失败: %v", err)
		http.Error(w, "创建备份文件失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 写入新配置
	// logger.LogPrintf("写入新配置到文件: %s", configPath)
	if err := os.WriteFile(configPath, newData, 0644); err != nil {
		// 恢复备份文件
		// logger.LogPrintf("错误：写入配置文件失败，尝试恢复备份: %v", err)
		os.WriteFile(configPath, data, 0644)
		http.Error(w, "写入配置文件失败，已恢复备份: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// logger.LogPrintf("配置文件写入成功")

	// 记录写入的配置内容中包含的代理组信息
	// logger.LogPrintf("写入的配置中包含 %d 个代理组", len(yamlProxyGroups))
	// for name, node := range yamlProxyGroups {
	// 	// logger.LogPrintf("代理组 [%s] 节点内容数量: %d", name, len(node.Content))
	// }

	// 返回成功响应
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("配置保存成功"))

	// logger.LogPrintf("代理组配置保存完成")
}
