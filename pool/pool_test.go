package pool

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
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

			probeAndApply(context.Background(), "test", g, nodes)
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

			probeAndApply(context.Background(), "retry", g, nodes)

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
