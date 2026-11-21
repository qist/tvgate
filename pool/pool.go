// Package pool 维护 proxygroups 中 pool 的动态节点。
//
// 池节点只在内存中存在，不写回配置文件；探测结果通过 ProxyGroupConfig.SetPoolNodes
// 交给 lb 选路。前端访问地址和 m3u8 重写不感知代理池。
package pool

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/lb"
	"github.com/qist/tvgate/logger"
)

type probeResult struct {
	node    *config.ProxyConfig
	latency time.Duration
	status  int
	err     error
}

type runner struct {
	mu     sync.Mutex
	group  *config.ProxyGroupConfig
	key    string
	cancel context.CancelFunc
}

func (r *runner) setGroup(g *config.ProxyGroupConfig) {
	r.mu.Lock()
	r.group = g
	r.mu.Unlock()
}

func (r *runner) currentGroup() *config.ProxyGroupConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.group
}

var (
	globalMu sync.Mutex
	runners  = make(map[string]*runner)
)

// Sync 按当前配置启动/停止池探测任务。
// 池配置未变化的组只替换 group 指针，保留已有探测循环和节点状态。
func Sync(cfg *config.Config) {
	if cfg == nil {
		return
	}
	desired := make(map[string]*config.ProxyGroupConfig)
	for name, g := range cfg.ProxyGroups {
		if g != nil && g.Pool != nil && len(g.Pool.Servers) > 0 {
			desired[name] = g
		}
	}

	globalMu.Lock()
	defer globalMu.Unlock()

	for name, r := range runners {
		g, ok := desired[name]
		if !ok {
			r.mu.Lock()
			if r.cancel != nil {
				r.cancel()
			}
			r.mu.Unlock()
			delete(runners, name)
			logger.LogPrintf("🛑 代理池 %s 已停止", name)
			continue
		}
		if r.key == g.PoolKey() {
			ensureInitialNodes(g)
			r.setGroup(g)
			delete(desired, name)
			continue
		}
		r.mu.Lock()
		if r.cancel != nil {
			r.cancel()
		}
		r.mu.Unlock()
		delete(runners, name)
	}

	for name, g := range desired {
		// 同步放入首批候选：保证配置加载完成后第一个请求就有节点可选，
		// 不必等探测 goroutine 被调度。
		ensureInitialNodes(g)
		ctx, cancel := context.WithCancel(context.Background())
		r := &runner{group: g, key: g.PoolKey(), cancel: cancel}
		runners[name] = r
		go run(ctx, name, r)
		logger.LogPrintf("🚀 代理池 %s 已启动", name)
	}
}

// StopAll 停止所有池探测任务（测试/退出时使用）。
func StopAll() {
	globalMu.Lock()
	defer globalMu.Unlock()
	for name, r := range runners {
		r.mu.Lock()
		if r.cancel != nil {
			r.cancel()
		}
		r.mu.Unlock()
		delete(runners, name)
	}
}

func ensureInitialNodes(g *config.ProxyGroupConfig) {
	if g == nil || g.Pool == nil {
		return
	}
	// 已有池节点（热重载继承）时不覆盖。
	if len(g.ActiveProxies()) > len(g.Proxies) {
		return
	}
	nodes, err := g.BuildPoolNodes()
	if err != nil || len(nodes) == 0 {
		return
	}
	topN := g.Pool.TopN
	if topN <= 0 {
		topN = 30
	}
	g.SetPoolNodes(limitNodes(nodes, topN))
}

func run(ctx context.Context, name string, r *runner) {
	g := r.currentGroup()
	if g == nil || g.Pool == nil {
		return
	}

	nodes, err := g.BuildPoolNodes()
	if err != nil {
		logger.LogPrintf("❌ 代理池 %s 展开失败: %v", name, err)
		return
	}
	if len(nodes) == 0 {
		logger.LogPrintf("⚠️ 代理池 %s 没有候选节点", name)
		return
	}

	topN := g.Pool.TopN
	if topN <= 0 {
		topN = 30
	}
	quickN := g.Pool.ProbeConcurrency
	if quickN <= 0 {
		quickN = 16
	}

	// 已有继承来的可用节点时不要用前 N 个候选覆盖。
	if len(g.ActiveProxies()) <= len(g.Proxies) {
		g.SetPoolNodes(limitNodes(nodes, topN))
	}

	if !g.Pool.EffectiveProbeEnabled() {
		logger.LogPrintf("ℹ️ 代理池 %s 探测已关闭，候选交由请求时按需测速", name)
		return
	}

	probeURL := g.Pool.ProbeURL
	if probeURL == "" {
		logger.LogPrintf("⚠️ 代理池 %s 未配置 probe_url，仅使用前 %d 个候选，不做定时探测", name, topN)
		return
	}

	// 冷启动先快速探测一小批，尽快给出可用节点。
	quick := limitNodes(nodes, quickN)
	probeAndApply(ctx, name, g, quick)

	interval := g.Pool.ProbeInterval
	if interval < 10*time.Second {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cur := r.currentGroup()
			if cur == nil || cur.Pool == nil {
				return
			}
			// 配置未变化时 cur 与 g 是同一个指针；变化时由 Sync 重启 runner。
			probeAndApply(ctx, name, cur, nodes)
		}
	}
}

func probeAndApply(ctx context.Context, name string, g *config.ProxyGroupConfig, nodes []*config.ProxyConfig) {
	if g == nil || g.Pool == nil || len(nodes) == 0 {
		return
	}
	if g.Pool.ProbeURL == "" {
		return
	}
	concurrency := g.Pool.ProbeConcurrency
	if concurrency <= 0 {
		concurrency = 16
	}
	if concurrency > 64 {
		concurrency = 64
	}

	results := probeAll(ctx, g, nodes, concurrency)
	good := make([]*config.ProxyConfig, 0, len(results))
	now := time.Now()
	cooldown := g.Pool.FailCooldown
	if cooldown <= 0 {
		cooldown = 60 * time.Second
	}

	if g.Stats == nil {
		g.Stats = &config.GroupStats{ProxyStats: make(map[string]*config.ProxyStats)}
	}
	latency := make(map[string]time.Duration, len(results))
	g.Stats.Lock()
	for _, res := range results {
		st := g.Stats.ProxyStats[res.node.Name]
		if st == nil {
			st = &config.ProxyStats{}
			g.Stats.ProxyStats[res.node.Name] = st
		}
		st.LastCheck = now
		if res.err == nil {
			st.Alive = true
			st.ResponseTime = res.latency
			st.StatusCode = res.status
			st.FailCount = 0
			st.CooldownUntil = time.Time{}
			latency[res.node.Name] = res.latency
			good = append(good, res.node)
		} else {
			st.Alive = false
			st.FailCount++
			st.StatusCode = res.status
			st.CooldownUntil = now.Add(cooldown)
		}
	}
	g.Stats.Unlock()

	sort.Slice(good, func(i, j int) bool {
		return latency[good[i].Name] < latency[good[j].Name]
	})
	topN := g.Pool.TopN
	if topN <= 0 {
		topN = 30
	}
	good = limitNodes(good, topN)

	if len(good) > 0 {
		g.SetPoolNodes(good)
		logger.LogPrintf("✅ 代理池 %s 探测完成: 可用 %d 个，最快 %s", name, len(good), good[0].Name)
		return
	}

	// 全部失败时保留上次可用列表；如果是首次探测，则放前 N 个候选给 lb 按需重试，
	// 避免因为探测目标临时不可达就完全放弃池。
	if len(g.ActiveProxies()) <= len(g.Proxies) {
		g.SetPoolNodes(limitNodes(nodes, topN))
	}
	logger.LogPrintf("⚠️ 代理池 %s 本轮探测无可用节点", name)
}

func probeAll(ctx context.Context, g *config.ProxyGroupConfig, nodes []*config.ProxyConfig, concurrency int) []probeResult {
	if concurrency <= 0 {
		concurrency = 1
	}
	results := make([]probeResult, 0, len(nodes))
	resultCh := make(chan probeResult, len(nodes))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

loop:
	for _, n := range nodes {
		n := n
		select {
		case <-ctx.Done():
			break loop
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			resultCh <- probeOne(ctx, g, n)
		}()
	}
	wg.Wait()
	close(resultCh)
	for res := range resultCh {
		results = append(results, res)
	}
	return results
}

// probeOne 复用 lb.CheckTarget（HTTP/RTSP 同一套直播检测），
// UA 取代理组 probe_ua → 全局 player.ua → 不发送。
func probeOne(ctx context.Context, g *config.ProxyGroupConfig, node *config.ProxyConfig) probeResult {
	res := probeResult{node: node}
	if ctx.Err() != nil {
		res.err = ctx.Err()
		return res
	}
	if g == nil || g.Pool == nil {
		res.err = fmt.Errorf("代理池配置不存在")
		return res
	}
	timeout := g.Pool.ProbeTimeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	retries := g.Pool.ProbeRetries
	if retries < 0 {
		retries = 0
	}
	if retries > 5 {
		retries = 5
	}
	ua := lb.GroupProbeUA(g)

	// 单轮探测内按 probe_retries 立即重试：动态端口偶发握手慢/抖动时，
	// 不因为一次失败就等下一个 probe_interval。
	for attempt := 0; attempt <= retries; attempt++ {
		if ctx.Err() != nil {
			res.err = ctx.Err()
			return res
		}
		probeCtx, cancel := context.WithTimeout(ctx, timeout)
		out := lb.CheckTarget(probeCtx, *node, g.Pool.ProbeURL, g.IPv6, ua)
		cancel()

		res.latency = out.ResponseTime
		res.status = out.StatusCode
		switch {
		case out.Err != nil:
			res.err = out.Err
		// 池探测必须拿到正常响应：4xx/5xx 是错误页，不能算可用节点。
		case out.StatusCode >= 400 || out.StatusCode == 0:
			res.err = fmt.Errorf("HTTP %d", out.StatusCode)
		default:
			res.err = nil
			return res
		}

		if attempt < retries {
			select {
			case <-ctx.Done():
				res.err = ctx.Err()
				return res
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
	return res
}

func limitNodes(nodes []*config.ProxyConfig, n int) []*config.ProxyConfig {
	if n <= 0 || len(nodes) <= n {
		return append([]*config.ProxyConfig(nil), nodes...)
	}
	return append([]*config.ProxyConfig(nil), nodes[:n]...)
}
