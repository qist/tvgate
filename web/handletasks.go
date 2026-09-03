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
	"github.com/qist/tvgate/tasks"
	"gopkg.in/yaml.v3"
)

// taskFieldOrder 控制保存到 YAML 时字段的固定顺序
var taskFieldOrder = []string{
	"name", "enabled", "group", "cron", "command", "timeout", "notes",
}

// handleTasksConfig 处理定时任务配置获取请求（返回任务列表，按 group 排序）
func (h *ConfigHandler) handleTasksConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	config.CfgMu.RLock()
	taskList := config.Cfg.Tasks
	config.CfgMu.RUnlock()

	items := make([]map[string]interface{}, 0, len(taskList))
	for _, t := range taskList {
		items = append(items, map[string]interface{}{
			"name":    t.Name,
			"enabled": t.Enabled,
			"group":   t.Group,
			"cron":    t.Cron,
			"command": t.Command,
			"timeout": formatDuration(t.Timeout),
			"notes":   t.Notes,
		})
	}
	_ = json.NewEncoder(w).Encode(items)
}

// handleTasksConfigSave 处理定时任务配置保存请求（整体替换 tasks 列表）
func (h *ConfigHandler) handleTasksConfigSave(w http.ResponseWriter, r *http.Request) {
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
	if err := replaceTasksConfigNode(&fullNode, entries); err != nil {
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

	// 更新内存配置（配置写入会触发 watch 热加载重启定时任务，此处同步保证即时生效）
	config.CfgMu.Lock()
	config.Cfg.Tasks = make([]config.TaskConfig, 0, len(entries))
	for _, e := range entries {
		var t config.TaskConfig
		applyTaskConfigFields(&t, e)
		config.Cfg.Tasks = append(config.Cfg.Tasks, t)
	}
	config.CfgMu.Unlock()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "success", "message": "配置保存成功"})
}

// replaceTasksConfigNode 整体替换 YAML 中的 tasks 节点为条目序列（不存在则创建）。
func replaceTasksConfigNode(node *yaml.Node, entries []map[string]interface{}) error {
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
		for _, key := range taskFieldOrder {
			if v, ok := e[key]; ok && v != nil {
				// 空字符串字段（如 timeout 等时长/可选字段）不写入，
				// 避免 YAML 出现 `timeout: ""`，空串无法反序列化进 time.Duration
				if s, isStr := v.(string); isStr && s == "" {
					continue
				}
				updateField(item, key, v)
			}
		}
		seq.Content = append(seq.Content, item)
	}

	// 查找并替换 tasks 节点
	for i := 0; i < len(root.Content); i += 2 {
		keyNode := root.Content[i]
		if keyNode.Kind == yaml.ScalarNode && keyNode.Value == "tasks" {
			root.Content[i+1] = seq
			return nil
		}
	}
	// 不存在则创建
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "tasks"}
	root.Content = append(root.Content, keyNode, seq)
	return nil
}

// handleTasksStatus 返回全部任务的运行时状态（下次执行时间、最近执行结果）。
func (h *ConfigHandler) handleTasksStatus(w http.ResponseWriter, r *http.Request) {
	config.CfgMu.RLock()
	statuses := tasks.TaskStatuses(&config.Cfg)
	config.CfgMu.RUnlock()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(statuses)
}

// handleTasksRun 立即执行指定任务命令一次（供前端"立即执行"按钮调用）。
// 请求体: {"command":"...","timeout":"60s"}，timeout 可空（不限时）。
func (h *ConfigHandler) handleTasksRun(w http.ResponseWriter, r *http.Request) {
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

	var req struct {
		Command string `json:"command"`
		Timeout string `json:"timeout"`
		Key     string `json:"key"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "解析JSON失败: "+err.Error(), http.StatusBadRequest)
		return
	}
	req.Command = strings.TrimSpace(req.Command)
	if req.Command == "" {
		http.Error(w, "执行命令不能为空", http.StatusBadRequest)
		return
	}

	var timeout time.Duration
	if req.Timeout != "" {
		if d, err := time.ParseDuration(req.Timeout); err == nil {
			timeout = d
		}
	}

	output, dur, err := tasks.ExecuteOnce(req.Command, timeout)
	// 联动登记到该任务卡片的状态（握手时用 key 关联）
	tasks.RecordRun(strings.TrimSpace(req.Key), err == nil, dur, summarizeOutput(output, err))
	success := err == nil
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  success,
		"duration": dur.String(),
		"output":   output,
		"error":    errString(err),
	})
}

// errString 返回错误信息，无错误时为空字符串
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// summarizeOutput 生成执行结果摘要（输出合并 / 错误信息），用于状态卡展示。
func summarizeOutput(out string, err error) string {
	if err != nil {
		return "失败: " + err.Error()
	}
	s := strings.TrimSpace(out)
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

// applyTaskConfigFields 将前端提交的单个条目 JSON 字段应用到 TaskConfig
func applyTaskConfigFields(t *config.TaskConfig, m map[string]interface{}) {
	if v, ok := m["name"].(string); ok {
		t.Name = v
	}
	if v, ok := m["enabled"].(bool); ok {
		t.Enabled = v
	}
	if v, ok := m["group"].(string); ok {
		t.Group = v
	}
	if v, ok := m["cron"].(string); ok {
		t.Cron = v
	}
	if v, ok := m["command"].(string); ok {
		t.Command = v
	}
	if v, ok := m["timeout"].(string); ok {
		if d, err := time.ParseDuration(v); err == nil {
			t.Timeout = d
		}
	}
	if v, ok := m["notes"].(string); ok {
		t.Notes = v
	}
}
