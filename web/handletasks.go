package web

import (
	"encoding/json"
	"fmt"
	"github.com/qist/tvgate/config/load"
	"github.com/qist/tvgate/logger"
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

	// 安全策略：危险命令不允许保存（删根/格式化磁盘/关机/下载即执行等）。
	// 运行期 tasks.runCommand 还会再校验一次，防止手工改配置文件绕过。
	for _, e := range entries {
		cmd := strings.TrimSpace(fmt.Sprintf("%v", e["command"]))
		if cmd == "" || cmd == "<nil>" {
			continue
		}
		if err := tasks.ValidateCommand(cmd); err != nil {
			label := strings.TrimSpace(fmt.Sprintf("%v", e["name"]))
			if label == "" || label == "<nil>" {
				label = cmd
			}
			if len(label) > 40 {
				label = label[:40] + "..."
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 400,
				"msg":  fmt.Sprintf("任务「%s」保存被拒绝：%v", label, err),
			})
			return
		}
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
	if _, err := backupConfigFile(configPath, updatedData); err != nil {
		http.Error(w, "创建备份文件失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(configPath, updatedData, 0644); err != nil {
		http.Error(w, "写入配置文件失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 立即重载内存配置：各 GET API 读 config.Cfg，磁盘变更要等 fsnotify
	// 防抖（reload: 5 秒）后才重新加载——保存后不等防抖，前端立即读到新值
	if err := load.LoadConfig(configPath); err != nil {
		logger.LogPrintf("保存后重载配置失败: %v", err)
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
	req.Key = strings.TrimSpace(req.Key)
	if req.Key == "" {
		http.Error(w, "缺少任务标识（key）", http.StatusBadRequest)
		return
	}

	// 只允许执行配置中已保存的任务命令，防止「立即执行」接口变成任意命令执行入口：
	// 否则拿到后台会话后可直接 POST 任意 command，绕过任务配置直接执行 shell。
	savedCmd, savedTimeout, ok := lookupSavedTask(req.Key, req.Command)
	if !ok {
		http.Error(w, "只能执行已保存的定时任务命令，请先保存任务配置", http.StatusForbidden)
		return
	}

	output, dur, err := tasks.ExecuteOnce(savedCmd, savedTimeout)
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

// lookupSavedTask 在已保存的任务配置中按 key 查找任务命令与超时。
// key 约定为任务名；任务名为空时用命令本身（与前端 keyOf 保持一致）。
// 用于「立即执行」：命令一律取自配置，不接受请求体里自带的任意命令。
func lookupSavedTask(key, command string) (string, time.Duration, bool) {
	config.CfgMu.RLock()
	defer config.CfgMu.RUnlock()
	for _, t := range config.Cfg.Tasks {
		k := t.Name
		if k == "" {
			k = t.Command
		}
		if k != key {
			continue
		}
		if strings.TrimSpace(t.Command) == "" {
			return "", 0, false
		}
		return t.Command, t.Timeout, true
	}
	return "", 0, false
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
