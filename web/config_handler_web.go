package web

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"github.com/qist/tvgate/config/load"
	"github.com/qist/tvgate/logger"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/qist/tvgate/config"
	"gopkg.in/yaml.v3"
)

// handleWebConfig 获取当前 Web 配置
func (h *ConfigHandler) handleWebConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	config.CfgMu.RLock()
	webCfg := config.Cfg.Web
	config.CfgMu.RUnlock()

	weak, weakHint := weakPasswordState()
	resp := map[string]interface{}{
		"enabled":  webCfg.Enabled,
		"username": webCfg.Username,
		// 不回传明文口令：页面留空保存表示「保持原密码不变」
		"password":           "",
		"password_set":       webCfg.Password != "",
		"password_weak":      weak,
		"password_weak_hint": weakHint,
		"path":               webCfg.Path,
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, "序列化配置失败: "+err.Error(), http.StatusInternalServerError)
	}
}

// handleWebConfigSave 保存 Web 配置
func (h *ConfigHandler) handleWebConfigSave(w http.ResponseWriter, r *http.Request) {
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

	var webConfig map[string]interface{}
	if err := json.Unmarshal(body, &webConfig); err != nil {
		http.Error(w, "解析JSON失败: "+err.Error(), http.StatusBadRequest)
		return
	}

	// 读取当前配置快照，用于空口令时保留原密码
	config.CfgMu.RLock()
	webCfg := config.Cfg.Web
	config.CfgMu.RUnlock()

	// 部分更新保护：请求未包含的字段沿用当前配置，避免只改某一项时把 web 段清空
	if _, ok := webConfig["enabled"]; !ok {
		webConfig["enabled"] = webCfg.Enabled
	}
	if _, ok := webConfig["username"]; !ok {
		webConfig["username"] = webCfg.Username
	}
	if _, ok := webConfig["path"]; !ok {
		webConfig["path"] = webCfg.Path
	}
	if _, ok := webConfig["password"]; !ok {
		webConfig["password"] = ""
	}

	// 修改口令必须验证当前口令：会话被劫持（或有人用公开初始口令登录）后，
	// 不能直接改密把真正的管理员锁在外面。
	newPassword := strings.TrimSpace(fmt.Sprintf("%v", webConfig["password"]))
	curPassword := strings.TrimSpace(webCfg.Password)
	passwordChanged := newPassword != "" && newPassword != curPassword
	if passwordChanged && curPassword != "" {
		if !h.acquireAuthAttempt(w, r, "chpasswd") {
			return
		}
		oldPw := strings.TrimSpace(fmt.Sprintf("%v", webConfig["old_password"]))
		if subtle.ConstantTimeCompare([]byte(oldPw), []byte(curPassword)) != 1 {
			h.recordAuthFailure(r, "chpasswd", webCfg.Username)
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 403,
				"msg":  "当前密码不正确：修改密码需要输入现有密码确认",
			})
			return
		}
		h.resetAuthFailures(r, "chpasswd")
	}

	// 弱口令期间只允许改密码：禁止同时改用户名/访问路径/启用状态。
	// 否则拿到公开默认口令的人可以先改密码、再改路径，把真正的管理员锁在外面。
	if weak, _ := weakPasswordState(); weak {
		var changed []string
		if v, ok := webConfig["enabled"]; ok && fmt.Sprintf("%v", v) != fmt.Sprintf("%v", webCfg.Enabled) {
			changed = append(changed, "enabled")
		}
		if v, ok := webConfig["username"]; ok && fmt.Sprintf("%v", v) != webCfg.Username {
			changed = append(changed, "username")
		}
		if v, ok := webConfig["path"]; ok && fmt.Sprintf("%v", v) != webCfg.Path {
			changed = append(changed, "path")
		}
		if len(changed) > 0 {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 400,
				"msg":  "弱口令期间只允许修改密码（本次同时修改了 " + strings.Join(changed, "、") + "），请先设置强密码",
			})
			return
		}
	}

	// 新口令强度校验：仅在提交了非空口令时校验（空值 = 保持原密码不变）。
	// 弱口令（默认 admin / 与用户名相同 / 过短 / 常见弱口令）一律拒绝保存，
	// 否则「弱口令锁定高危操作」会一直生效、后台无法正常使用。
	checkUser := strings.TrimSpace(webCfg.Username)
	if v, ok := webConfig["username"]; ok {
		if s := strings.TrimSpace(fmt.Sprintf("%v", v)); s != "" {
			checkUser = s
		}
	}
	if v, ok := webConfig["password"]; ok {
		newPw := strings.TrimSpace(fmt.Sprintf("%v", v))
		if newPw != "" {
			if weak, hint := isWeakWebPassword(checkUser, newPw); weak {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"code": 400,
					"msg": "密码强度不足：" + hint +
						fmt.Sprintf("。请设置至少 %d 位、且不同于用户名和默认密码的口令", minWebPasswordLen),
				})
				return
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

	// 查找并更新 web 节点
	if fullNode.Kind == yaml.DocumentNode && len(fullNode.Content) > 0 {
		doc := fullNode.Content[0]
		if doc.Kind == yaml.MappingNode {
			webFound := false
			for i := 0; i < len(doc.Content); i += 2 {
				keyNode := doc.Content[i]
				if keyNode.Kind == yaml.ScalarNode && keyNode.Value == "web" {
					newWebNode := &yaml.Node{Kind: yaml.MappingNode}

					if enabled, ok := webConfig["enabled"]; ok {
						newWebNode.Content = append(newWebNode.Content,
							&yaml.Node{Kind: yaml.ScalarNode, Value: "enabled"},
							&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", enabled)})
					}
					if username, ok := webConfig["username"]; ok {
						newWebNode.Content = append(newWebNode.Content,
							&yaml.Node{Kind: yaml.ScalarNode, Value: "username"},
							&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", username)})
					}
					if password, ok := webConfig["password"]; ok {
						// 空口令加固：若提交值为空，保留原配置密码而不是覆盖为空，
						// 避免误操作将 web 口令置空（配合登录侧拒绝空口令）
						if fmt.Sprintf("%v", password) == "" {
							password = webCfg.Password
						}
						newWebNode.Content = append(newWebNode.Content,
							&yaml.Node{Kind: yaml.ScalarNode, Value: "password"},
							&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", password)})
					}
					if path, ok := webConfig["path"]; ok {
						newWebNode.Content = append(newWebNode.Content,
							&yaml.Node{Kind: yaml.ScalarNode, Value: "path"},
							&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", path)})
					}

					doc.Content[i+1] = newWebNode
					webFound = true
					break
				}
			}

			// 没找到 web 节点就新建
			if !webFound {
				newWebNode := &yaml.Node{Kind: yaml.MappingNode}
				doc.Content = append(doc.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "web"},
					newWebNode)
			}
		}
	}

	newData, err := yaml.Marshal(&fullNode)
	if err != nil {
		http.Error(w, "序列化配置失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if _, err := backupConfigFile(configPath, newData); err != nil {
		http.Error(w, "创建备份文件失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err := os.WriteFile(configPath, newData, 0644); err != nil {
		os.WriteFile(configPath, data, 0644)
		http.Error(w, "写入配置失败，已恢复备份: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 改密成功：删除首次启动时生成的初始口令文件，避免过期口令长期留在磁盘上
	if passwordChanged {
		config.ClearInitialPasswordFile()
	}

	// 立即重载内存配置：各 GET API 读 config.Cfg，磁盘变更要等 fsnotify
	// 防抖（reload: 5 秒）后才重新加载——保存后不等防抖，前端立即读到新值
	if err := load.LoadConfig(configPath); err != nil {
		logger.LogPrintf("保存后重载配置失败: %v", err)
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte("配置保存成功"))
}
