package handler

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qist/tvgate/config"
)

// forwardProxy 是一个最小 HTTP 正向代理：接收绝对 URI 请求并转发，
// 记录经过它的路径与 Referer，用于断言重定向后的请求仍走代理。
type forwardProxy struct {
	mu       sync.Mutex
	seen     []string
	referers map[string]string
	client   *http.Client
}

func newForwardProxy() *forwardProxy {
	return &forwardProxy{
		referers: map[string]string{},
		client: &http.Client{
			Transport: &http.Transport{Proxy: nil},
			// 代理必须把 302 原样回传给调用方，由调用方决定是否跟随；
			// 若代理自己跟随，就会掩盖调用方「重定向是否走代理」的验证。
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (p *forwardProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.seen = append(p.seen, r.URL.Path)
	p.referers[r.URL.Path] = r.Header.Get("Referer")
	p.mu.Unlock()

	req, err := http.NewRequest(r.Method, r.RequestURI, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req.Header = r.Header.Clone()
	req.Header.Del("Proxy-Connection")
	resp, err := p.client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (p *forwardProxy) hits(path string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, s := range p.seen {
		if s == path {
			n++
		}
	}
	return n
}

func (p *forwardProxy) referer(path string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.referers[path]
}

// TestFetchViaProxyGroupRedirectThroughProxy：302 重定向后的请求必须继续走同一代理，
// 且订阅内显式配置的 Referer 原样透传保留（301/302 解析型源的核心诉求）。
func TestFetchViaProxyGroupRedirectThroughProxy(t *testing.T) {
	b := false
	oldInsecure := config.Cfg.HTTP.InsecureSkipVerify
	config.Cfg.HTTP.InsecureSkipVerify = &b
	defer func() { config.Cfg.HTTP.InsecureSkipVerify = oldInsecure }()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, "/final.m3u8", http.StatusFound)
		case "/final.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = w.Write([]byte("#EXTM3U\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	fp := newForwardProxy()
	proxySrv := httptest.NewServer(fp)
	defer proxySrv.Close()

	host, portStr, err := net.SplitHostPort(strings.TrimPrefix(proxySrv.URL, "http://"))
	if err != nil {
		t.Fatalf("解析测试代理解析失败: %v", err)
	}
	port, _ := strconv.Atoi(portStr)
	pc := &config.ProxyConfig{Name: "local-http", Type: "http", Server: host, Port: port}
	pg := &config.ProxyGroupConfig{
		Proxies:     []*config.ProxyConfig{pc},
		LoadBalance: "fastest",
		Interval:    time.Minute,
		RetryDelay:  time.Millisecond,
		Stats:       &config.GroupStats{ProxyStats: map[string]*config.ProxyStats{}},
	}

	hdr := http.Header{}
	hdr.Set("User-Agent", "test-ua")
	hdr.Set("Referer", "https://v.example.com/")
	resp, usedPg, err := FetchViaProxyGroup(context.Background(), upstream.URL+"/start", hdr, false, pg)
	if err != nil {
		t.Fatalf("FetchViaProxyGroup err: %v", err)
	}
	if resp == nil {
		t.Fatal("未选中代理（resp=nil）")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("应 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "#EXTM3U") {
		t.Fatalf("响应体不是 m3u8: %q", string(body))
	}
	if usedPg != pg {
		t.Fatalf("usedPg 应为内联代理组: %v", usedPg)
	}

	// 关键断言：初始请求与 302 后的 /final.m3u8 都经过代理
	if fp.hits("/start") == 0 {
		t.Fatalf("初始请求未走代理: %v", fp.seen)
	}
	if fp.hits("/final.m3u8") == 0 {
		t.Fatalf("302 重定向后的请求未走代理: %v", fp.seen)
	}
	// 显式 Referer 在重定向后仍保留
	if got := fp.referer("/final.m3u8"); got != "https://v.example.com/" {
		t.Fatalf("重定向后 Referer 应保留显式配置值, got %q", got)
	}
}
