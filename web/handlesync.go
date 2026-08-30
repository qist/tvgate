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

// syncFieldOrder 控制保存到 YAML 时字段的固定顺序
var syncFieldOrder = []string{
	"name", "enabled", "type", "repo", "branch", "token",
	"interval", "repo_path", "local_path", "only_php", "backup", "delete", "timeout",
}

// handleSyncEditor 处理仓库同步配置编辑器页面
func (h *ConfigHandler) handleSyncEditor(w http.ResponseWriter, r *http.Request) {
	webPath := h.getWebPath()
	data := map[string]interface{}{
		"title":   "TVGate 仓库同步配置编辑器",
		"webPath": webPath,
	}
	if err := h.renderTemplate(w, r, "sync_editor", "templates/sync_editor.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
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
