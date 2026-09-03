package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/updater"
	"gopkg.in/yaml.v3"
)

// syncFieldOrder 控制保存到 YAML 时字段的固定顺序
var syncFieldOrder = []string{
	"name", "enabled", "type", "host", "repo", "branch", "token",
	"interval", "repo_path", "local_path", "only_php", "backup", "delete", "timeout",
}

// handleSyncConfig 处理仓库同步配置获取请求（返回仓库列表）
func (h *ConfigHandler) handleSyncConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	config.CfgMu.RLock()
	syncList := config.Cfg.Sync
	config.CfgMu.RUnlock()

	items := make([]map[string]interface{}, 0, len(syncList))
	for _, s := range syncList {
		items = append(items, map[string]interface{}{
			"name":       s.Name,
			"enabled":    s.Enabled,
			"type":       s.Type,
			"host":       s.Host,
			"repo":       s.Repo,
			"branch":     s.Branch,
			"token":      maskToken(s.Token), // 令牌不回显真值，仅掩码占位（前端填写新值可见，保存后不可再看）
			"interval":   formatDuration(s.Interval),
			"repo_path":  s.RepoPath,
			"local_path": s.LocalPath,
			"only_php":   s.OnlyPHP,
			"backup":     boolOr(s.Backup, true),
			"delete":     boolOr(s.Delete, false),
			"protect":    s.Protect,
			"timeout":    formatDuration(s.Timeout),
		})
	}
	_ = json.NewEncoder(w).Encode(items)
}

// maskToken 返回凭据掩码占位符（仅在已配置时），与 global_auth 的 credentialMask 一致
func maskToken(v string) string {
	if v == "" {
		return ""
	}
	return credentialMask
}

// resolveSyncTokens 将前端提交条目中的掩码占位 token 替换为原始值（未改动则保留原配置，不回显不覆盖）
func resolveSyncTokens(entries []map[string]interface{}, old []config.SyncConfig) []map[string]interface{} {
	resolved := make([]map[string]interface{}, len(entries))
	for i, e := range entries {
		ne := make(map[string]interface{}, len(e))
		for k, v := range e {
			ne[k] = v
		}
		if t, ok := ne["token"].(string); ok && t == credentialMask {
			if i < len(old) {
				ne["token"] = old[i].Token
			} else {
				ne["token"] = ""
			}
		}
		resolved[i] = ne
	}
	return resolved
}

// handleSyncConfigSave 处理仓库同步配置保存请求（整体替换 sync 列表）
func (h *ConfigHandler) handleSyncConfigSave(w http.ResponseWriter, r *http.Request) {
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

	var entries []map[string]interface{}
	if err := json.Unmarshal(body, &entries); err != nil {
		http.Error(w, "解析JSON失败: "+err.Error(), http.StatusBadRequest)
		return
	}

	// 快照原始配置，掩码占位 token 需保留原值（不回显不覆盖）
	config.CfgMu.RLock()
	oldSync := config.Cfg.Sync
	config.CfgMu.RUnlock()
	entries = resolveSyncTokens(entries, oldSync)

	// 读取配置文件（yaml.Node 保留注释与格式）
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
	if err := replaceSyncConfigNode(&fullNode, entries); err != nil {
		http.Error(w, "更新配置失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	updatedData, err := yaml.Marshal(&fullNode)
	if err != nil {
		http.Error(w, "序列化配置失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 备份后写入
	backupPath := configPath + ".backup." + time.Now().Format("20060102150405")
	if err := os.WriteFile(backupPath, data, 0644); err != nil {
		http.Error(w, "创建备份文件失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(configPath, updatedData, 0644); err != nil {
		http.Error(w, "写入配置文件失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 更新内存配置（配置写入会触发 watch 热加载重启 sync，此处同步保证即时生效）
	config.CfgMu.Lock()
	config.Cfg.Sync = make([]config.SyncConfig, 0, len(entries))
	for _, e := range entries {
		var s config.SyncConfig
		applySyncConfigFields(&s, e)
		config.Cfg.Sync = append(config.Cfg.Sync, s)
	}
	config.CfgMu.Unlock()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "success", "message": "配置保存成功"})
}

// replaceSyncConfigNode 整体替换 YAML 中的 sync 节点为条目序列（不存在则创建）。
// 每个条目由前端的 map 构造为 yaml.MappingNode，字段显式带 Tag 防 !!null 误判。
func replaceSyncConfigNode(node *yaml.Node, entries []map[string]interface{}) error {
	if node.Kind != yaml.DocumentNode || len(node.Content) == 0 {
		return fmt.Errorf("无效的YAML文档节点")
	}
	root := node.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("根节点不是映射节点")
	}

	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, e := range entries {
		item := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, key := range syncFieldOrder {
			if v, ok := e[key]; ok && v != nil {
				updateField(item, key, v)
			}
		}
		if v, ok := e["protect"].([]interface{}); ok {
			updateStringArrayField(item, "protect", v)
		}
		seq.Content = append(seq.Content, item)
	}

	// 查找并替换 sync 节点
	for i := 0; i < len(root.Content); i += 2 {
		keyNode := root.Content[i]
		if keyNode.Kind == yaml.ScalarNode && keyNode.Value == "sync" {
			root.Content[i+1] = seq
			return nil
		}
	}
	// 不存在则创建
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "sync"}
	root.Content = append(root.Content, keyNode, seq)
	return nil
}

// applySyncConfigFields 将前端提交的单个条目 JSON 字段应用到 SyncConfig
func applySyncConfigFields(sync *config.SyncConfig, m map[string]interface{}) {
	if v, ok := m["name"].(string); ok {
		sync.Name = v
	}
	if v, ok := m["enabled"].(bool); ok {
		sync.Enabled = v
	}
	if v, ok := m["type"].(string); ok {
		sync.Type = v
	}
	if v, ok := m["host"].(string); ok {
		sync.Host = v
	}
	if v, ok := m["repo"].(string); ok {
		sync.Repo = v
	}
	if v, ok := m["branch"].(string); ok {
		sync.Branch = v
	}
	if v, ok := m["token"].(string); ok {
		sync.Token = v
	}
	if v, ok := m["interval"].(string); ok {
		if d, err := time.ParseDuration(v); err == nil {
			sync.Interval = d
		}
	}
	if v, ok := m["repo_path"].(string); ok {
		sync.RepoPath = v
	}
	if v, ok := m["local_path"].(string); ok {
		sync.LocalPath = v
	}
	if v, ok := m["only_php"].(bool); ok {
		sync.OnlyPHP = v
	}
	if v, ok := m["backup"].(bool); ok {
		sync.Backup = &v
	}
	if v, ok := m["delete"].(bool); ok {
		sync.Delete = &v
	}
	if v, ok := m["timeout"].(string); ok {
		if d, err := time.ParseDuration(v); err == nil {
			sync.Timeout = d
		}
	}
	if v, ok := m["protect"].([]interface{}); ok {
		sync.Protect = make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				sync.Protect = append(sync.Protect, s)
			}
		}
	}
}

// boolOr 返回 *bool 指向的值，nil 时返回默认值
func boolOr(b *bool, def bool) bool {
	if b == nil {
		return def
	}
	return *b
}

// handleSyncBranches 获取指定仓库的分支列表（供前端下拉选择）。
// token 为掩码占位时从已存配置解析真实 token（只读，不回显）。
func (h *ConfigHandler) handleSyncBranches(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	typ := q.Get("type")
	if typ == "" {
		typ = "github"
	}
	repo := q.Get("repo")
	if repo == "" {
		http.Error(w, "请先填写仓库标识", http.StatusBadRequest)
		return
	}
	host := q.Get("host")
	token := q.Get("token")
	if token == credentialMask {
		config.CfgMu.RLock()
		for _, s := range config.Cfg.Sync {
			if s.Repo == repo && (s.Type == typ || s.Type == "") {
				token = s.Token
				break
			}
		}
		config.CfgMu.RUnlock()
	}

	branches, err := fetchBranches(typ, host, repo, token)
	if err != nil {
		http.Error(w, "获取分支失败: "+err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(branches)
}

// fetchBranches 按平台拉取仓库分支列表（独立实现，避免 web ↔ sync 循环依赖）。
// 仅 GitHub 使用 github 加速配置；GitLab/Gitee（含自建 host）一律直连，不附加加速。
func fetchBranches(typ, host, repo, token string) ([]string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	var urls []string
	var headers map[string]string

	switch typ {
	case "gitlab":
		if host == "" {
			host = "https://gitlab.com"
		}
		urls = []string{strings.TrimSuffix(host, "/") + "/api/v4/projects/" + url.PathEscape(repo) + "/repository/branches?per_page=100"}
		if token != "" {
			headers = map[string]string{"PRIVATE-TOKEN": token}
		}
	case "gitee":
		if host == "" {
			host = "https://gitee.com"
		}
		u := strings.TrimSuffix(host, "/") + "/api/v5/repos/" + repo + "/branches?per_page=100"
		if token != "" {
			u += "&access_token=" + url.QueryEscape(token)
		}
		urls = []string{u}
	default: // github —— 仅此平台使用 github 加速配置，其余平台不附加
		apiPath := "https://api.github.com/repos/" + repo + "/branches?per_page=100"
		urls = []string{apiPath}
		config.CfgMu.RLock()
		gc := config.Cfg.Github
		config.CfgMu.RUnlock()
		if gc.Enabled {
			var accel []string
			if gc.URL != "" {
				accel = append(accel, updater.BuildURL(gc.URL, apiPath))
			}
			for _, b := range gc.BackupURLs {
				if b != "" {
					accel = append(accel, updater.BuildURL(b, apiPath))
				}
			}
			urls = append(accel, urls...)
		}
		if token != "" {
			headers = map[string]string{"Authorization": "Bearer " + token}
		}
	}

	var lastErr error
	for _, u := range urls {
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("User-Agent", "TVGate-Sync")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("请求分支失败: HTTP %d %s", resp.StatusCode, truncateBytes(body, 200))
			continue
		}
		var items []struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(body, &items); err != nil {
			lastErr = err
			continue
		}
		names := make([]string, 0, len(items))
		for _, it := range items {
			if it.Name != "" {
				names = append(names, it.Name)
			}
		}
		return names, nil
	}
	return nil, lastErr
}

// truncateBytes 截断响应体用于错误信息
func truncateBytes(b []byte, n int) string {
	s := string(b)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
