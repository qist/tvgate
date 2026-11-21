package pool

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qist/tvgate/config"
)

func TestProbeAndApplyHTTPProxy(t *testing.T) {
	restore := prepareTestConfig(t)
	defer restore()

	cases := []struct {
		name      string
		groupUA   string
		playerUA  string
		wantUA    string
		wantHasUA bool
	}{
		{name: "group probe_ua", groupUA: "okhttp-test", playerUA: "player-ua", wantUA: "okhttp-test", wantHasUA: true},
		{name: "fallback player.ua", playerUA: "player-ua", wantUA: "player-ua", wantHasUA: true},
		{name: "no ua header", wantUA: "", wantHasUA: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config.CfgMu.Lock()
			config.Cfg.Player.UA = tc.playerUA
			config.CfgMu.Unlock()

			target, closeTarget := startProbeTarget(t)
			defer closeTarget()
			proxyAddr, closeProxy := startConnectProxy(t)
			defer closeProxy()

			g := &config.ProxyGroupConfig{
				// local: HTTP 代理走自定义 CONNECT，测试代理只需实现 CONNECT
				Resolve: "local",
				ProbeUA: tc.groupUA,
				Pool: &config.ProxyPoolConfig{
					Servers:  []string{proxyAddr},
					Protocol: "http",
					ProbeURL: target.url,
					TopN:     5,
				},
			}
			g.Pool.SetDefaults()
			g.Stats = &config.GroupStats{ProxyStats: make(map[string]*config.ProxyStats)}

			nodes, err := g.BuildPoolNodes()
			if err != nil {
				t.Fatalf("BuildPoolNodes: %v", err)
			}
			if len(nodes) != 1 {
				t.Fatalf("nodes = %d, want 1", len(nodes))
			}

			probeAndApply(context.Background(), "test", g, nodes, false)
			active := g.ActiveProxies()
			if len(active) != 1 {
				t.Fatalf("探测后可用节点 = %d, want 1", len(active))
			}
			g.Stats.RLock()
			st := g.Stats.ProxyStats[active[0].Name]
			g.Stats.RUnlock()
			if st == nil || !st.Alive || st.ResponseTime <= 0 {
				t.Fatalf("探测统计异常: %+v", st)
			}

			gotUA, hasUA := target.lastUA()
			if hasUA != tc.wantHasUA || gotUA != tc.wantUA {
				t.Fatalf("探测 UA = %q (存在=%v), want %q (存在=%v)", gotUA, hasUA, tc.wantUA, tc.wantHasUA)
			}
		})
	}
}

// TestProbeRetriesWithinRound 验证 probe_retries 在同一轮探测内立即重试：
// 首个 CONNECT 返回 502 时，重试 1 次即可恢复；不重试则本轮判失败。
func TestProbeRetriesWithinRound(t *testing.T) {
	restore := prepareTestConfig(t)
	defer restore()

	cases := []struct {
		name      string
		retries   int
		failFirst int
		wantAlive bool
		wantHits  int
	}{
		{name: "no retry", retries: 0, failFirst: 1, wantAlive: false, wantHits: 1},
		{name: "retry recovers", retries: 1, failFirst: 1, wantAlive: true, wantHits: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target, closeTarget := startProbeTarget(t)
			defer closeTarget()
			proxyAddr, hits, closeProxy := startFlakyConnectProxy(t, tc.failFirst)
			defer closeProxy()

			g := &config.ProxyGroupConfig{
				Resolve: "local",
				Pool: &config.ProxyPoolConfig{
					Servers:      []string{proxyAddr},
					Protocol:     "http",
					ProbeURL:     target.url,
					TopN:         5,
					ProbeRetries: tc.retries,
				},
			}
			g.Pool.SetDefaults()
			g.Stats = &config.GroupStats{ProxyStats: make(map[string]*config.ProxyStats)}
			nodes, err := g.BuildPoolNodes()
			if err != nil {
				t.Fatalf("BuildPoolNodes: %v", err)
			}

			probeAndApply(context.Background(), "retry", g, nodes, false)

			g.Stats.RLock()
			st := g.Stats.ProxyStats[nodes[0].Name]
			g.Stats.RUnlock()
			alive := st != nil && st.Alive
			if alive != tc.wantAlive {
				t.Fatalf("alive = %v, want %v (stat=%+v)", alive, tc.wantAlive, st)
			}
			if got := hits(); got != tc.wantHits {
				t.Fatalf("探测拨号次数 = %d, want %d", got, tc.wantHits)
			}
		})
	}
}

func prepareTestConfig(t *testing.T) func() {
	t.Helper()
	config.CfgMu.Lock()
	oldInsecure := config.Cfg.HTTP.InsecureSkipVerify
	oldUA := config.Cfg.Player.UA
	insecure := false
	config.Cfg.HTTP.InsecureSkipVerify = &insecure
	config.CfgMu.Unlock()
	return func() {
		config.CfgMu.Lock()
		config.Cfg.HTTP.InsecureSkipVerify = oldInsecure
		config.Cfg.Player.UA = oldUA
		config.CfgMu.Unlock()
	}
}

type probeTarget struct {
	url string

	mu    sync.Mutex
	ua    string
	hasUA bool
}

func (p *probeTarget) lastUA() (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ua, p.hasUA
}

func startProbeTarget(t *testing.T) (*probeTarget, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	pt := &probeTarget{url: "http://" + ln.Addr().String() + "/main.m3u8"}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua, ok := r.Header["User-Agent"]
		pt.mu.Lock()
		pt.hasUA = ok
		if ok && len(ua) > 0 {
			pt.ua = ua[0]
		}
		pt.mu.Unlock()
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-VERSION:3\n")
	})}
	go srv.Serve(ln)
	return pt, func() {
		_ = srv.Close()
	}
}

// startFlakyConnectProxy 启动一个前 failFirst 次 CONNECT 返回 502 的代理，
// 用于验证探测重试。返回代理地址、已接受连接数、关闭函数。
func startFlakyConnectProxy(t *testing.T, failFirst int) (string, func() int, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	served := 0
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			served++
			n := served
			mu.Unlock()
			go func(c net.Conn, n int) {
				if n <= failFirst {
					_, _ = fmt.Fprint(c, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
					_ = c.Close()
					return
				}
				handleConnect(c)
			}(conn, n)
		}
	}()
	return ln.Addr().String(), func() int {
			mu.Lock()
			defer mu.Unlock()
			return served
		}, func() {
			_ = ln.Close()
		}
}

// startConnectProxy 启动一个最小 HTTP CONNECT 代理，用于验证探测链路。
func startConnectProxy(t *testing.T) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-done:
					return
				default:
					return
				}
			}
			go handleConnect(conn)
		}
	}()
	return ln.Addr().String(), func() {
		close(done)
		_ = ln.Close()
	}
}

func handleConnect(client net.Conn) {
	defer client.Close()
	br := bufio.NewReader(client)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	if req.Method != http.MethodConnect {
		_, _ = fmt.Fprint(client, "HTTP/1.1 405 Method Not Allowed\r\n\r\n")
		return
	}
	upstream, err := net.DialTimeout("tcp", req.Host, 3*time.Second)
	if err != nil {
		_, _ = fmt.Fprint(client, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
		return
	}
	defer upstream.Close()
	_, _ = fmt.Fprint(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
	go func() { _, _ = io.Copy(upstream, br) }()
	_, _ = io.Copy(client, upstream)
}

// TestSyncImmediatelyReprobesOnProbeConfigChange 验证池结构不变、仅探测参数
// （如 probe_ua）变化时，也要立即测一轮，而不是等 probe_interval 到点。
func TestSyncImmediatelyReprobesOnProbeConfigChange(t *testing.T) {
	restore := prepareTestConfig(t)
	defer restore()

	target, closeTarget := startProbeTarget(t)
	defer closeTarget()
	proxyAddr, closeProxy := startConnectProxy(t)
	defer closeProxy()

	StopAll()
	defer StopAll()

	stats := &config.GroupStats{ProxyStats: make(map[string]*config.ProxyStats)}
	newGroup := func(ua string) *config.ProxyGroupConfig {
		g := &config.ProxyGroupConfig{
			Resolve: "local",
			ProbeUA: ua,
			Stats:   stats,
			Pool: &config.ProxyPoolConfig{
				Servers:       []string{proxyAddr},
				Protocol:      "http",
				ProbeURL:      target.url,
				ProbeInterval: time.Hour,
				TopN:          5,
			},
		}
		g.Pool.SetDefaults()
		return g
	}

	g1 := newGroup("ua-a")
	cfg := &config.Config{ProxyGroups: map[string]*config.ProxyGroupConfig{"g": g1}}
	Sync(cfg)

	nodes := g1.PoolNodes()
	if len(nodes) == 0 {
		t.Fatal("Sync 后没有初始节点")
	}
	nodeName := nodes[0].Name

	waitProbe := func(after time.Time) time.Time {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			stats.RLock()
			st := stats.ProxyStats[nodeName]
			var last time.Time
			if st != nil {
				last = st.LastCheck
			}
			stats.RUnlock()
			if last.After(after) {
				return last
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("等待立即测速超时（after=%v）", after)
		return time.Time{}
	}

	first := waitProbe(time.Time{})

	// PoolKey 不变（servers/probe_url/probe_interval 均相同），只有 probe_ua 变化。
	g2 := newGroup("ua-b")
	cfg.ProxyGroups["g"] = g2
	Sync(cfg)

	if got := waitProbe(first); !got.After(first) {
		t.Fatalf("配置变更后未立即重新测速: first=%v got=%v", first, got)
	}
}

// TestProbeSampleReadsHLSThroughput 验证探测不再只读 2KB：m3u8 会继续取首个
// 分片并读最多 2MB，写入 Throughput；这是避免「延迟低但带宽不够」节点被选中的关键。
func TestProbeSampleReadsHLSThroughput(t *testing.T) {
	restore := prepareTestConfig(t)
	defer restore()

	var segmentHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/main.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:10\n#EXTINF:10,\nseg.ts\n")
		case "/seg.ts":
			segmentHits.Add(1)
			w.Header().Set("Content-Type", "video/mp2t")
			w.Header().Set("Content-Length", "307200")
			_, _ = w.Write(make([]byte, 300<<10))
		default:
			http.NotFound(w, r)
		}
	}))
	defer target.Close()

	proxyAddr, closeProxy := startConnectProxy(t)
	defer closeProxy()

	g := &config.ProxyGroupConfig{
		Resolve: "local",
		Stats:   &config.GroupStats{ProxyStats: make(map[string]*config.ProxyStats)},
		Pool: &config.ProxyPoolConfig{
			Servers:      []string{proxyAddr},
			Protocol:     "http",
			ProbeURL:     target.URL + "/main.m3u8",
			ProbeTimeout: 5 * time.Second,
			TopN:         5,
		},
	}
	g.Pool.SetDefaults()
	nodes, err := g.BuildPoolNodes()
	if err != nil {
		t.Fatalf("BuildPoolNodes: %v", err)
	}

	probeAndApply(context.Background(), "throughput", g, nodes, false)

	g.Stats.RLock()
	st := g.Stats.ProxyStats[nodes[0].Name]
	g.Stats.RUnlock()
	if st == nil || !st.Alive {
		t.Fatalf("探测未成功: %+v", st)
	}
	if st.Throughput <= 0 {
		t.Fatalf("未记录吞吐样本: %+v", st)
	}
	if segmentHits.Load() == 0 {
		t.Fatal("探测没有继续请求 m3u8 里的分片")
	}
}

// TestProbeInUseNodeNeverUpgradesThroughput 验证播放中的节点只允许下调吞吐：
// 空闲时完整采样（8MB/后段速率）已经把「前段突发、随后停住」的出口判低，
// 播放中的 1MB 短采样落在突发段里测出高值，也不能把它重新抬上去，
// 否则又会选中这种跑一会儿就卡的节点。
func TestProbeInUseNodeNeverUpgradesThroughput(t *testing.T) {
	restore := prepareTestConfig(t)
	defer restore()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp2t")
		_, _ = w.Write(make([]byte, 1<<20))
	}))
	defer target.Close()
	proxyAddr, closeProxy := startConnectProxy(t)
	defer closeProxy()

	g := &config.ProxyGroupConfig{
		Resolve: "local",
		Stats:   &config.GroupStats{ProxyStats: make(map[string]*config.ProxyStats)},
		Pool: &config.ProxyPoolConfig{
			Servers:      []string{proxyAddr},
			Protocol:     "http",
			ProbeURL:     target.URL,
			ProbeTimeout: 5 * time.Second,
			TopN:         5,
		},
	}
	g.Pool.SetDefaults()
	nodes, err := g.BuildPoolNodes()
	if err != nil || len(nodes) == 0 {
		t.Fatalf("BuildPoolNodes: %v", err)
	}
	name := nodes[0].Name
	// 完整采样已经判定只有 1KB/s（突发后停住），播放中的短采样必须不能抬高它。
	const verified = int64(1 << 10)
	g.Stats.ProxyStats[name] = &config.ProxyStats{
		LastUsed:   time.Now(),
		LastCheck:  time.Now(),
		Throughput: verified,
	}

	res := probeOne(context.Background(), g, nodes[0], false)
	if !res.inUse {
		t.Fatal("最近被选路使用的节点应标记为播放中")
	}
	if res.err != nil {
		t.Fatalf("探测失败: %v", res.err)
	}
	if res.throughput != -1 {
		t.Fatalf("播放中只验连通，应返回无样本 -1，实际 %d", res.throughput)
	}

	probeAndApply(context.Background(), "inuse", g, nodes, false)

	g.Stats.RLock()
	st := g.Stats.ProxyStats[name]
	g.Stats.RUnlock()
	if st == nil || !st.Alive {
		t.Fatalf("探测后节点应可用: %+v", st)
	}
	if st.Throughput != verified {
		t.Fatalf("播放中的短采样不得抬高已验证吞吐 %d，实际 %d", verified, st.Throughput)
	}
}

// TestProbeUpdatesThroughput 验证深度探测会真正采样吞吐并覆盖旧分：
// 节点变快/变慢都必须以本轮实测为准，不能一直沿用历史值。
func TestProbeUpdatesThroughput(t *testing.T) {
	restore := prepareTestConfig(t)
	defer restore()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp2t")
		_, _ = w.Write(make([]byte, 1<<20))
	}))
	defer target.Close()
	proxyAddr, closeProxy := startConnectProxy(t)
	defer closeProxy()

	g := &config.ProxyGroupConfig{
		Resolve: "local",
		Stats:   &config.GroupStats{ProxyStats: make(map[string]*config.ProxyStats)},
		Pool: &config.ProxyPoolConfig{
			Servers:      []string{proxyAddr},
			Protocol:     "http",
			ProbeURL:     target.URL,
			ProbeTimeout: 5 * time.Second,
			TopN:         5,
		},
	}
	g.Pool.SetDefaults()
	nodes, err := g.BuildPoolNodes()
	if err != nil || len(nodes) == 0 {
		t.Fatalf("BuildPoolNodes: %v", err)
	}
	// 旧的高分（模拟节点此前很快），深度探测必须用本轮实测覆盖它。
	const oldThroughput = int64(8 << 20)
	g.Stats.ProxyStats[nodes[0].Name] = &config.ProxyStats{
		Alive:      true,
		Throughput: oldThroughput,
	}

	if alive := probeAndApply(context.Background(), "deep", g, nodes, false); alive != len(nodes) {
		t.Fatalf("深度探测应保留可用节点，got %d", alive)
	}

	g.Stats.RLock()
	st := g.Stats.ProxyStats[nodes[0].Name]
	g.Stats.RUnlock()
	if st == nil || !st.Alive {
		t.Fatalf("深度探测后节点应可用: %+v", st)
	}
	if st.Throughput <= 0 {
		t.Fatal("深度探测应采样吞吐")
	}
	if st.Throughput == oldThroughput {
		t.Fatalf("深度探测应覆盖旧吞吐，实际仍为 %d", st.Throughput)
	}
}

// TestGroupInPlayback 验证没有播放时深度探测会被跳过：
// 判定只看节点的 LastUsed 是否落在组的 interval 窗口内。
func TestGroupInPlayback(t *testing.T) {
	g := &config.ProxyGroupConfig{
		Interval: time.Minute,
		Stats:    &config.GroupStats{ProxyStats: make(map[string]*config.ProxyStats)},
		Proxies: []*config.ProxyConfig{
			{Name: "p1", Type: "http", Server: "127.0.0.1", Port: 1},
		},
	}
	now := time.Now()

	if groupInPlayback(g, now) {
		t.Fatal("从未使用的组应判定为空闲")
	}

	g.Stats.ProxyStats["p1"] = &config.ProxyStats{LastUsed: now.Add(-30 * time.Second)}
	if !groupInPlayback(g, now) {
		t.Fatal("LastUsed 在 interval 窗口内应判定为播放中")
	}

	g.Stats.ProxyStats["p1"].LastUsed = now.Add(-2 * time.Minute)
	if groupInPlayback(g, now) {
		t.Fatal("LastUsed 超出 interval 窗口应判定为空闲")
	}

	if groupInPlayback(&config.ProxyGroupConfig{}, now) {
		t.Fatal("没有 Stats 的组应判定为空闲")
	}
}

// TestProbeTargetsRetriesFailedNodes 验证一次失败不会让节点消失到下次全量扫描：
// 深度探测会继续重测近期失败、次数未达上限的候选。
func TestProbeTargetsRetriesFailedNodes(t *testing.T) {
	n1 := &config.ProxyConfig{Name: "good", Type: "http", Server: "127.0.0.1", Port: 1}
	n2 := &config.ProxyConfig{Name: "flaky", Type: "http", Server: "127.0.0.1", Port: 2}
	g := &config.ProxyGroupConfig{
		Stats: &config.GroupStats{ProxyStats: map[string]*config.ProxyStats{
			"good":  {Alive: true, LastCheck: time.Now()},
			"flaky": {Alive: false, LastCheck: time.Now(), FailCount: 1},
		}},
	}
	g.SetPoolNodes([]*config.ProxyConfig{n1})
	all := []*config.ProxyConfig{n1, n2}

	got := probeTargets(g, all, 10)
	if len(got) != 2 {
		t.Fatalf("失败 1 次的节点应继续重试，got %d", len(got))
	}

	g.Stats.ProxyStats["flaky"].FailCount = probeRetryLimit
	if got := probeTargets(g, all, 10); len(got) != 1 {
		t.Fatalf("连续失败达上限后本应暂停重试，got %d", len(got))
	}

	// 超过退避间隔后仍要重新纳入重试：出口恢复后不能一直等到全端口扫描/重启。
	g.Stats.ProxyStats["flaky"].LastCheck = time.Now().Add(-probeRetryBackoff - time.Second)
	if got := probeTargets(g, all, 10); len(got) != 2 {
		t.Fatalf("超过退避间隔后应重新重试，got %d", len(got))
	}
}

// TestResetProbeFailures 验证全端口扫描前会清空失败计数与冷却，
// 避免「测速失败的数据没重置」，端口恢复后仍被视为冷却/失败。
func TestResetProbeFailures(t *testing.T) {
	n1 := &config.ProxyConfig{Name: "p1", Type: "http", Server: "127.0.0.1", Port: 1}
	g := &config.ProxyGroupConfig{
		Stats: &config.GroupStats{ProxyStats: map[string]*config.ProxyStats{
			"p1": {Alive: false, FailCount: 5, CooldownUntil: time.Now().Add(time.Hour), LastCheck: time.Now()},
		}},
	}
	resetProbeFailures(g, []*config.ProxyConfig{n1})
	st := g.Stats.ProxyStats["p1"]
	if st.FailCount != 0 || !st.CooldownUntil.IsZero() {
		t.Fatalf("失败状态未重置: %+v", st)
	}
}

// TestLightProbeDoesNotSampleThroughput 验证空闲期全端口扫描只做连通性检查：
// 不断言吞吐（沿用旧值），也不继续请求 m3u8 里的分片（没有测速流量）。
func TestLightProbeDoesNotSampleThroughput(t *testing.T) {
	restore := prepareTestConfig(t)
	defer restore()

	var segmentHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/main.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:10\n#EXTINF:10,\nseg.ts\n")
		default:
			segmentHits.Add(1)
			_, _ = w.Write(make([]byte, 300<<10))
		}
	}))
	defer target.Close()

	proxyAddr, closeProxy := startConnectProxy(t)
	defer closeProxy()

	g := &config.ProxyGroupConfig{
		Resolve: "local",
		Stats:   &config.GroupStats{ProxyStats: make(map[string]*config.ProxyStats)},
		Pool: &config.ProxyPoolConfig{
			Servers:  []string{proxyAddr},
			Protocol: "http",
			ProbeURL: target.URL + "/main.m3u8",
		},
	}
	g.Pool.SetDefaults()
	nodes, err := g.BuildPoolNodes()
	if err != nil || len(nodes) == 0 {
		t.Fatalf("BuildPoolNodes: %v", err)
	}
	const oldThroughput = int64(4 << 20)
	g.Stats.ProxyStats[nodes[0].Name] = &config.ProxyStats{
		Alive:      true,
		Throughput: oldThroughput,
		LastCheck:  time.Now(),
	}

	probeAndApply(context.Background(), "light", g, nodes, true)

	st := g.Stats.ProxyStats[nodes[0].Name]
	if !st.Alive {
		t.Fatalf("轻量扫描应判定节点可用: %+v", st)
	}
	if st.Throughput != oldThroughput {
		t.Fatalf("轻量扫描不应改写吞吐: got %d want %d", st.Throughput, oldThroughput)
	}
	if segmentHits.Load() != 0 {
		t.Fatalf("轻量扫描不应继续请求分片（实际 %d 次）", segmentHits.Load())
	}
}

// TestProxyInUseUsesShortWindow 验证「在用」判定跟随 45s 静默窗口，而不是组的
// interval（可能是 3 分钟）：播放停止后该节点要尽快恢复完整采样，拿到吞吐样本。
func TestProxyInUseUsesShortWindow(t *testing.T) {
	g := &config.ProxyGroupConfig{
		Interval: 3 * time.Minute,
		Stats: &config.GroupStats{ProxyStats: map[string]*config.ProxyStats{
			"p1": {LastUsed: time.Now().Add(-60 * time.Second)},
		}},
	}
	if proxyInUse(g, "p1", time.Now()) {
		t.Fatal("静默 60s 的节点不应再判定为播放中（interval=3m 不应延长窗口）")
	}

	g.Stats.ProxyStats["p1"].LastUsed = time.Now().Add(-10 * time.Second)
	if !proxyInUse(g, "p1", time.Now()) {
		t.Fatal("10s 前还在传输的节点应判定为播放中")
	}
}
