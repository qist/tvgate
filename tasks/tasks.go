package tasks

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	gosync "sync"
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
)

// manager 单个定时任务的调度循环
type manager struct {
	cfg    *config.TaskConfig
	cancel context.CancelFunc
}

var (
	mu       gosync.Mutex
	managers []*manager

	statusMu  gosync.Mutex
	statusMap = map[string]*TaskStatus{} // key = name（name 为空则用 command）
)

// TaskStatus 单个任务的运行时状态（供 Web 端查询展示）
type TaskStatus struct {
	Key         string        `json:"key"` // 标识（name，为空则 command）
	NextRun     time.Time     `json:"next_run"`
	Ran         bool          `json:"ran"`           // 是否已执行过
	Success     bool          `json:"success"`       // 最近一次是否成功
	LastRun     time.Time     `json:"last_run"`      // 最近一次执行时间
	LastDur     time.Duration `json:"last_duration"` // 最近一次耗时
	LastMessage string        `json:"last_message"`  // 最近一次输出/错误摘要
}

// statusKey 计算任务在状态表中的标识
func statusKey(t *config.TaskConfig) string {
	if t.Name != "" {
		return t.Name
	}
	return t.Command
}

func setStatus(key string, mutate func(*TaskStatus)) {
	statusMu.Lock()
	st := statusMap[key]
	if st == nil {
		st = &TaskStatus{Key: key}
		statusMap[key] = st
	}
	mutate(st)
	statusMu.Unlock()
}

// TaskStatuses 返回当前配置全部任务的运行时状态（按配置顺序）。
func TaskStatuses(c *config.Config) []TaskStatus {
	statusMu.Lock()
	defer statusMu.Unlock()
	out := make([]TaskStatus, 0, len(c.Tasks))
	for i := range c.Tasks {
		t := &c.Tasks[i]
		key := statusKey(t)
		if st := statusMap[key]; st != nil {
			copy := *st
			copy.Key = key
			out = append(out, copy)
			continue
		}
		out = append(out, TaskStatus{Key: key})
	}
	return out
}

// RecordRun 记录一次手动执行的运行结果到状态表（供 Web 端"立即执行"按钮联动展示）。
func RecordRun(key string, success bool, dur time.Duration, msg string) {
	if key == "" {
		return
	}
	setStatus(key, func(st *TaskStatus) {
		st.Ran, st.Success, st.LastRun, st.LastDur, st.LastMessage = true, success, time.Now(), dur, msg
	})
}

// Start 启动（或替换全部已有实例）定时任务调度器。
// 每次调用既停止旧实例并按最新配置重启，供程序启动与配置热加载共用。
func Start(c *config.Config) {
	mu.Lock()
	defer mu.Unlock()

	// 停止全部旧实例
	for _, m := range managers {
		if m.cancel != nil {
			m.cancel()
		}
	}
	managers = nil

	if len(c.Tasks) == 0 {
		return
	}
	for i := range c.Tasks {
		entry := &c.Tasks[i]
		if !entry.Enabled || entry.Command == "" {
			continue
		}
		if _, err := parseCron(entry.Cron); err != nil {
			logger.LogPrintf("⚠️ [tasks] 任务 %q 的 cron 表达式无效已跳过: %v", taskLabel(entry), err)
			continue
		}
		ctx, cancel := context.WithCancel(config.ServerCtx)
		m := &manager{cfg: entry, cancel: cancel}
		managers = append(managers, m)
		go m.loop(ctx, entry)
		logger.LogPrintf("🚀 [tasks] 已启动: %s (cron=%s, group=%s)", taskLabel(entry), entry.Cron, entry.Group)
	}
}

// taskLabel 生成任务标识（有 name 时用 name，否则取 command 片段）
func taskLabel(t *config.TaskConfig) string {
	if t.Name != "" {
		return t.Name
	}
	s := t.Command
	if len(s) > 40 {
		s = s[:40] + "..."
	}
	return s
}

// loop 主循环：计算下一次 cron 触发时间并休眠等待，到点执行一次，然后继续轮询下一次。
// 每次触发后立即重读配置中的下一次时间，天然支持下一次调度的向前推进。
func (m *manager) loop(ctx context.Context, t *config.TaskConfig) {
	key := statusKey(t)
	for {
		next := nextTick(ctx, t, time.Now())
		if next.IsZero() {
			logger.LogPrintf("🛑 [tasks] %s 无可排程时间，任务退出", taskLabel(t))
			return
		}
		setStatus(key, func(st *TaskStatus) { st.NextRun = next })
		logger.LogPrintf("🕒 [tasks] %s 下次执行: %s", taskLabel(t), next.Local().Format("2006-01-02 15:04:05"))
		select {
		case <-ctx.Done():
			logger.LogPrintf("🛑 [tasks] %s 已停止", taskLabel(t))
			return
		case <-time.After(time.Until(next)):
			m.runTask(ctx, t, key)
		}
	}
}

// nextTick 计算 next 之后的下一次触发时间；无效表达式或 ctx 取消时返回零值。
func nextTick(ctx context.Context, t *config.TaskConfig, from time.Time) (next time.Time) {
	select {
	case <-ctx.Done():
		return time.Time{}
	default:
	}
	e, err := parseCron(t.Cron)
	if err != nil {
		return time.Time{}
	}
	return e.next(from)
}

// runTask 执行一次任务命令（经系统 shell），记录耗时与结果。
func (m *manager) runTask(ctx context.Context, t *config.TaskConfig, key string) {
	cmdCtx := ctx
	var cancel context.CancelFunc
	if t.Timeout > 0 {
		cmdCtx, cancel = context.WithTimeout(ctx, t.Timeout)
		defer cancel()
	}

	start := time.Now()
	out, err := shellCommand(cmdCtx, t.Command)
	dur := time.Since(start).Round(time.Millisecond)
	now := time.Now()
	if err != nil {
		if ctx.Err() != nil {
			logger.LogPrintf("🛑 [tasks] %s 命令执行被中断(%s)", taskLabel(t), dur)
			return
		}
		msg := string(out)
		if len(msg) > 300 {
			msg = msg[:300] + "..."
		}
		logger.LogPrintf("❌ [tasks] %s 执行失败(%s): %v — %s", taskLabel(t), dur, err, msg)
		setStatus(key, func(st *TaskStatus) {
			st.Ran, st.Success, st.LastRun, st.LastDur, st.LastMessage = true, false, now, dur, summarize(msg, err)
		})
		return
	}
	logger.LogPrintf("✅ [tasks] %s 执行成功(%s)", taskLabel(t), dur)
	setStatus(key, func(st *TaskStatus) {
		st.Ran, st.Success, st.LastRun, st.LastDur, st.LastMessage = true, true, now, dur, summarize(string(out), nil)
	})
}

// summarize 生成最近一次执行的结果摘要（输出/错误前若干字符）
func summarize(out string, err error) string {
	if err != nil {
		return "失败: " + err.Error()
	}
	s := strings.TrimSpace(out)
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

// ExecuteOnce 立即执行一次命令（供 Web 端"立即执行"按钮调用）。
// command 经系统 shell 执行；timeout>0 时整体限时，返回合并输出、耗时与错误。
func ExecuteOnce(command string, timeout time.Duration) (output string, duration time.Duration, err error) {
	ctx := config.ServerCtx
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	start := time.Now()
	out, e := shellCommand(ctx, command)
	return string(out), time.Since(start).Round(time.Millisecond), e
}

// shellCommand 通过系统默认 shell 执行命令并返回 stdout+stderr。
func shellCommand(ctx context.Context, command string) ([]byte, error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	return cmd.CombinedOutput()
}
