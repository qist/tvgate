// Package pool 维护 proxygroups 中 pool 的动态节点。
//
// 池节点只在内存中存在，不写回配置文件；探测结果通过 ProxyGroupConfig.SetPoolNodes
// 交给 lb 选路。前端访问地址和 m3u8 重写不感知代理池。
package pool

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/lb"
	"github.com/qist/tvgate/logger"
)

// minHTTPProbeTimeout 是 HTTP 探测的实际超时下限。
// 吞吐按「最后 3 秒」统计，只有观察窗口足够长，才能看到出口在突发之后
// 是否限速或直接停住（实测有的出口前 3 秒跑 7Mbps、6 秒后完全不再传数据）。
// 配置的 probe_timeout 小于该值时，HTTP 探测仍按该值执行；RTSP 不受影响。
const minHTTPProbeTimeout = 8 * time.Second

// busyProbeSampleSize 是节点正在播放时的正文采样上限。
// 播放中只读 1 字节做连通性检查，不更新吞吐：
//   - 完整采样（8MB/8s）会和播放抢带宽；
//   - 短采样落在突发段里，测出的高值会把已判低的节点抬回去；
//   - 短采样落在限速段里，又会把正常节点误降。
//
// 播放中节点的吞吐沿用空闲时完整采样的结果；变慢/断流由真实请求失败
// （lb 标记 + 重试切换）兜底。
const busyProbeSampleSize = 1

type probeResult struct {
	node       *config.ProxyConfig
	latency    time.Duration
	throughput int64
	status     int
	err        error
	// inUse 表示节点本轮探测时正在承载播放：只验连通，吞吐沿用上次完整采样。
	inUse bool
}

type runner struct {
	mu     sync.Mutex
	group  *config.ProxyGroupConfig
	key    string
	sig    string
	cancel context.CancelFunc
	// wake 用于配置变更/手动触发时打断等待，立即执行一轮探测。
	wake chan struct{}
}

// updateGroup 替换 runner 使用的配置，并返回探测相关配置是否变化。
// 池结构未变（PoolKey 相同）但 probe_ua / ipv6 等探测参数变化时，
// 需要立即重测一次，否则会一直等到 probe_interval 到点。
func (r *runner) updateGroup(g *config.ProxyGroupConfig) bool {
	sig := probeSignature(g)
	r.mu.Lock()
	changed := r.sig != sig
	r.group = g
	r.sig = sig
	r.mu.Unlock()
	return changed
}

func (r *runner) triggerProbe() {
	select {
	case r.wake <- struct{}{}:
	default:
		// 已有一次待执行的立即探测，合并即可。
	}
}

// probeSignature 覆盖所有会影响探测行为、但不在 PoolKey 里的配置。
// 这些字段变化时节点列表不变，但必须立即重测一次。
func probeSignature(g *config.ProxyGroupConfig) string {
	if g == nil {
		return ""
	}
	return fmt.Sprintf("%s|%t|%s", g.PoolKey(), g.IPv6, lb.GroupProbeUA(g))
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
			if r.updateGroup(g) {
				// probe_ua / ipv6 等探测参数变化：立即补一轮，测完重新按 interval 计时。
				logger.LogPrintf("🔁 代理池 %s 探测配置变更，立即重新测速", name)
				r.triggerProbe()
			}
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
		r := &runner{
			group:  g,
			key:    g.PoolKey(),
			sig:    probeSignature(g),
			cancel: cancel,
			wake:   make(chan struct{}, 1),
		}
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

	// 两档节奏：
	//   - 深度探测（probe_interval，默认 60s）：对当前可用节点读取完整分片、
	//     按「后段速率」算吞吐，并对前 2 名再做一次采样取较小值；
	//   - 全端口扫描（probe_full_interval，默认 1 小时）：重新确认整个端口段。
	//
	// 深度探测只在「组内有播放」时运行：没有播放时持续采样既浪费流量，
	// 也会白白占用出口带宽。空闲期只保留全端口扫描，播放开始时由 lb 的
	// 请求时测速兜底，随后一个 probe_interval 内深度探测接管排名。
	lastFull := time.Now()
	runFull := func(cur *config.ProxyGroupConfig, reason string) {
		if cur == nil || cur.Pool == nil {
			return
		}
		logger.LogPrintf("🔍 代理池 %s 全端口扫描（%s）", name, reason)
		probeAndApply(ctx, name, cur, nodes)
		lastFull = time.Now()
	}

	// 启动时全量扫一次，避免要等一个 full_interval 才能发现端口段里的可用节点。
	runFull(g, "启动")

	activeNodes := func(cur *config.ProxyGroupConfig) []*config.ProxyConfig {
		return probeTargets(cur, nodes, topN)
	}

	deepTicker := time.NewTicker(poolDuration(g.Pool.ProbeInterval, 60*time.Second, 10*time.Second))
	defer deepTicker.Stop()
	fullTicker := time.NewTicker(poolDuration(g.Pool.ProbeFullInterval, time.Hour, time.Minute))
	defer fullTicker.Stop()

	// 节点全部不可用时兜底全量扫描；仍受 full_interval 限流，避免频繁扫端口段。
	failover := func(cur *config.ProxyGroupConfig, alive int) {
		if alive > 0 || cur == nil || cur.Pool == nil {
			return
		}
		if time.Since(lastFull) < poolDuration(cur.Pool.ProbeFullInterval, time.Hour, time.Minute) {
			return
		}
		runFull(cur, "全部节点不可用")
	}

	// playing 记录上一轮看到的播放状态，只在切换时打日志，避免反复刷屏。
	playing := false
	// checkPlayback 判断组内是否正在播放；返回 false 时跳过深度探测。
	// 判定依据是池节点的 LastUsed：lb 每次选路、真实请求都会刷新它，
	// 播放停止后超过组的 interval 自动判定为空闲。
	checkPlayback := func(cur *config.ProxyGroupConfig) bool {
		nowPlaying := groupInPlayback(cur, time.Now())
		switch {
		case nowPlaying && !playing:
			logger.LogPrintf("▶️ 代理池 %s 检测到播放，启用深度探测", name)
		case !nowPlaying && playing:
			logger.LogPrintf("💤 代理池 %s 当前无播放，暂停深度探测（仅保留全端口扫描）", name)
		}
		playing = nowPlaying
		return nowPlaying
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-deepTicker.C:
			cur := r.currentGroup()
			if cur == nil || cur.Pool == nil {
				return
			}
			if !checkPlayback(cur) {
				continue
			}
			good := probeAndApply(ctx, name, cur, activeNodes(cur))
			failover(cur, good)
		case <-fullTicker.C:
			cur := r.currentGroup()
			if cur == nil || cur.Pool == nil {
				return
			}
			runFull(cur, "定时")
		case <-r.wake:
			// 配置变更触发的立即探测：跑一轮全量，并重置计时，
			// 避免刚测完又立刻触发。
			cur := r.currentGroup()
			if cur == nil || cur.Pool == nil {
				return
			}
			runFull(cur, "配置变更")
			deepTicker.Reset(poolDuration(cur.Pool.ProbeInterval, 60*time.Second, 10*time.Second))
			fullTicker.Reset(poolDuration(cur.Pool.ProbeFullInterval, time.Hour, time.Minute))
		}
	}
}

// probeRetryLimit 是深度探测对「当前不可用」节点的最大重试轮数。
// 源站抖动/瞬时超时导致一次失败时，节点不应消失到下一次全端口扫描
// （默认 1 小时）；连续失败达到该次数才放弃，等全端口扫描重新确认。
const probeRetryLimit = 3

// probeTargets 返回本轮深度探测的节点：
//   - 当前可用节点；
//   - 曾经测过、但当前不在可用列表里的候选（失败次数 < probeRetryLimit）。
//
// 后者用于快速恢复：某节点因为一次超时被移出列表后，下一轮深度探测仍会
// 重测它，成功即可回到列表，避免「只剩一个差节点、一直选它」。
func probeTargets(g *config.ProxyGroupConfig, allNodes []*config.ProxyConfig, topN int) []*config.ProxyConfig {
	if g == nil {
		return nil
	}
	active := g.PoolNodes()
	if len(active) == 0 {
		active = limitNodes(allNodes, topN)
	}
	if g.Stats == nil {
		return active
	}

	seen := make(map[string]bool, len(active))
	out := make([]*config.ProxyConfig, 0, len(active)+4)
	for _, n := range active {
		seen[n.Name] = true
		out = append(out, n)
	}
	g.Stats.RLock()
	for _, n := range allNodes {
		if seen[n.Name] {
			continue
		}
		st := g.Stats.ProxyStats[n.Name]
		if st != nil && !st.LastCheck.IsZero() && st.FailCount < probeRetryLimit {
			out = append(out, n)
		}
	}
	g.Stats.RUnlock()
	return out
}

// poolDuration 返回配置值，未配置或过小时回退到默认/下限。
func poolDuration(v, def, min time.Duration) time.Duration {
	if v <= 0 {
		v = def
	}
	if v < min {
		v = min
	}
	return v
}

// probeConcurrency 返回本组探测并发，限制在 1~64。
func probeConcurrency(g *config.ProxyGroupConfig) int {
	concurrency := g.Pool.ProbeConcurrency
	if concurrency <= 0 {
		concurrency = 16
	}
	if concurrency > 64 {
		concurrency = 64
	}
	return concurrency
}

// probeAndApply 执行一轮深度探测（采样吞吐），返回可用节点数。
func probeAndApply(ctx context.Context, name string, g *config.ProxyGroupConfig, nodes []*config.ProxyConfig) int {
	if g == nil || g.Pool == nil || len(nodes) == 0 || g.Pool.ProbeURL == "" {
		return 0
	}
	results := probeAll(ctx, g, nodes, probeConcurrency(g))
	// 对吞吐最高的前 2 名再做一轮采样取较小值：单次采样仍可能有抖动。
	results = refineTopThroughput(ctx, g, results, 2)
	return applyProbeResults(name, g, nodes, results)
}

// applyProbeResults 写入一轮探测结果、按吞吐排序并替换池可用列表。
func applyProbeResults(name string, g *config.ProxyGroupConfig, nodes []*config.ProxyConfig, results []probeResult) int {
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
	throughput := make(map[string]int64, len(results))
	notes := make(map[string]string, len(results))
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
			// 播放中的节点只做连通性检查（1 字节），不更新吞吐：
			// 短采样既可能被突发段抬高、也可能被限速段压低，都会误导选路。
			// 无有效样本（-1）时同样保留旧吞吐，避免一次源站抖动把正常节点
			// 永久标成跑不动。从未测过的节点（LastCheck 为零）则直接采用本轮结果。
			measured := res.throughput
			if res.inUse || measured < 0 {
				measured = st.Throughput
			}
			st.Throughput = measured
			throughput[res.node.Name] = measured
			if res.inUse {
				notes[res.node.Name] = "（播放中，沿用上次完整采样）"
			}
			st.StatusCode = res.status
			st.FailCount = 0
			st.CooldownUntil = time.Time{}
			latency[res.node.Name] = res.latency
			good = append(good, res.node)
		} else {
			st.Alive = false
			st.Throughput = 0
			st.FailCount++
			st.StatusCode = res.status
			st.CooldownUntil = now.Add(cooldown)
		}
	}
	g.Stats.Unlock()

	// 有吞吐样本时优先按带宽排序，避免「延迟 300ms 但只有 1Mbps」的节点
	// 排在「延迟 400ms 但能跑 8Mbps」的前面；没有样本（m3u8 太小/RTSP）
	// 时继续按延迟排序。
	sort.Slice(good, func(i, j int) bool {
		ti, tj := throughput[good[i].Name], throughput[good[j].Name]
		if ti != tj {
			return ti > tj
		}
		return latency[good[i].Name] < latency[good[j].Name]
	})
	topN := g.Pool.TopN
	if topN <= 0 {
		topN = 30
	}
	good = limitNodes(good, topN)

	// 逐节点打印成功节点的延迟/吞吐：选路日志只会出现被选中的节点，
	// 不打印的话其它端口（例如吞吐第二的节点）这轮测了多少完全看不到。
	for _, n := range good {
		fmtTP := "-"
		if v := throughput[n.Name]; v > 0 {
			fmtTP = fmt.Sprintf("%.1fMbps", float64(v)*8/1e6)
		}
		logger.LogPrintf("📊 代理池 %s 节点: %-40s 延迟: %-10v 吞吐: %s%s",
			name, n.Name, latency[n.Name].Truncate(time.Millisecond), fmtTP, notes[n.Name])
	}

	if len(good) > 0 {
		g.SetPoolNodes(good)
		best := good[0].Name
		tp := throughput[best]
		if tp > 0 {
			logger.LogPrintf("✅ 代理池 %s 探测完成: 可用 %d 个，最快 %s（%.1f Mbps）",
				name, len(good), best, float64(tp)*8/1e6)
		} else {
			logger.LogPrintf("✅ 代理池 %s 探测完成: 可用 %d 个，最快 %s", name, len(good), best)
		}
		return len(good)
	}

	// 全部失败时保留上次可用列表；如果是首次探测，则放前 N 个候选给 lb 按需重试，
	// 避免因为探测目标临时不可达就完全放弃池。
	if len(g.ActiveProxies()) <= len(g.Proxies) {
		g.SetPoolNodes(limitNodes(nodes, topN))
	}
	logger.LogPrintf("⚠️ 代理池 %s 本轮探测无可用节点", name)
	return 0
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

// refineTopThroughput 对吞吐最高的 topK 个节点再做一轮采样，取两轮较小值。
// 出口常见「前段突发、随后限速」，单次采样会把 2Mbps 测成 6Mbps；
// 第二轮只更新吞吐，不因失败改判节点（瞬时抖动不应把节点踢掉）。
func refineTopThroughput(ctx context.Context, g *config.ProxyGroupConfig, results []probeResult, topK int) []probeResult {
	type cand struct {
		idx int
		tp  int64
	}
	cands := make([]cand, 0, len(results))
	for i := range results {
		// 播放中的节点不再做第二轮深采样，避免额外抢播放带宽。
		if results[i].err == nil && results[i].throughput > 0 && !results[i].inUse {
			cands = append(cands, cand{i, results[i].throughput})
		}
	}
	// 只有一个成功节点时没有可比对象，省掉第二轮流量。
	if topK <= 0 || len(cands) <= 1 {
		return results
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].tp > cands[j].tp })
	if len(cands) > topK {
		cands = cands[:topK]
	}

	var wg sync.WaitGroup
	for _, c := range cands {
		c := c
		wg.Add(1)
		go func() {
			defer wg.Done()
			second := probeOne(ctx, g, results[c.idx].node)
			if second.err != nil || second.throughput <= 0 {
				return
			}
			if second.throughput < results[c.idx].throughput {
				results[c.idx].throughput = second.throughput
			}
		}()
	}
	wg.Wait()
	return results
}

// proxyInUse 判断节点最近是否在承载播放。
// LastUsed 由 lb.SelectProxy 在每次选路时刷新，HLS 每个分片都会重新选路，
// 因此正在播放的节点会被持续判定为占用；播放停止后超过一个 interval 自动失效。
// 长连接（FLV/TS 直链）只在建连时选路一次，超过 interval 后可能被当作空闲，
// 这是当前统计粒度的已知边界。
func proxyInUse(g *config.ProxyGroupConfig, name string, now time.Time) bool {
	if g == nil || g.Stats == nil {
		return false
	}
	window := g.Interval
	if window <= 0 {
		window = 3 * time.Minute
	}
	g.Stats.RLock()
	st := g.Stats.ProxyStats[name]
	inUse := st != nil && !st.LastUsed.IsZero() && now.Sub(st.LastUsed) <= window
	g.Stats.RUnlock()
	return inUse
}

// groupInPlayback 判断组内是否有节点最近承载过播放。
// LastUsed 由 lb 选路与真实请求刷新；窗口取组的 interval，播放停止后
// 超过该窗口即视为空闲，深度探测随之暂停。
func groupInPlayback(g *config.ProxyGroupConfig, now time.Time) bool {
	if g == nil || g.Stats == nil {
		return false
	}
	window := g.Interval
	if window <= 0 {
		window = 3 * time.Minute
	}
	nodes := g.ActiveProxies()
	g.Stats.RLock()
	defer g.Stats.RUnlock()
	for _, p := range nodes {
		if p == nil {
			continue
		}
		st := g.Stats.ProxyStats[p.Name]
		if st != nil && !st.LastUsed.IsZero() && now.Sub(st.LastUsed) <= window {
			return true
		}
	}
	return false
}

// probeOne 探测单个节点：复用 lb.CheckTarget（HTTP/RTSP 同一套直播检测），
// 采样吞吐。UA 取代理组 probe_ua → 全局 player.ua → 不发送。
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
		// 与 config.SetDefaults 保持一致。
		timeout = 10 * time.Second
	}
	// HTTP 需要覆盖「突发段 + 后段观察窗」，否则又会被前段突发骗过。
	if !strings.HasPrefix(strings.ToLower(g.Pool.ProbeURL), "rtsp://") && timeout < minHTTPProbeTimeout {
		timeout = minHTTPProbeTimeout
	}
	retries := g.Pool.ProbeRetries
	if retries < 0 {
		retries = 0
	}
	if retries > 5 {
		retries = 5
	}
	ua := lb.GroupProbeUA(g)

	// 节点正在承载播放时，把它的实际在用带宽（播放流量）加回去，
	// 得到总容量估算；只冻结上一次的空闲吞吐会掩盖节点真实变慢。
	inUse := proxyInUse(g, node.Name, time.Now())
	res.inUse = inUse

	// 单轮探测内按 probe_retries 立即重试：动态端口偶发握手慢/抖动时，
	// 不因为一次失败就等下一个 probe_interval。
	for attempt := 0; attempt <= retries; attempt++ {
		if ctx.Err() != nil {
			res.err = ctx.Err()
			return res
		}
		probeCtx, cancel := context.WithTimeout(ctx, timeout)
		if inUse {
			// 播放中不采样正文：只读 1 字节验证连通，吞吐沿用上次完整采样。
			probeCtx = lb.WithProbeSampleSize(probeCtx, busyProbeSampleSize)
		}
		out := lb.CheckTarget(probeCtx, *node, g.Pool.ProbeURL, g.IPv6, ua)
		cancel()

		res.latency = out.ResponseTime
		res.status = out.StatusCode
		if out.Throughput < 0 {
			// 没有有效样本（播放中只读 1 字节 / 分片没读到数据 / 采样窗口不足）：
			// 不要把它当成 0，否则一次源站抖动就会把正常节点永久标成跑不动。
			res.throughput = -1
		} else {
			res.throughput = out.Throughput
		}
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
