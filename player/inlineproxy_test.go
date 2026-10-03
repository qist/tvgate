package player

import "testing"

func TestParseProxyURL(t *testing.T) {
	cases := []struct {
		in     string
		typ    string
		server string
		port   int
		user   string
		pass   string
	}{
		{"socks5://127.0.0.1:7890", "socks5", "127.0.0.1", 7890, "", ""},
		{"socks5://u:p@127.0.0.1:1080", "socks5", "127.0.0.1", 1080, "u", "p"},
		{"http://proxy.example.com:3128", "http", "proxy.example.com", 3128, "", ""},
		{"https://proxy.example.com", "https", "proxy.example.com", 443, "", ""},
		{"socks4://192.0.2.9:1080", "socks4", "192.0.2.9", 1080, "", ""},
		// 省略 scheme 默认 http；省略端口按类型取默认
		{"127.0.0.1:8888", "http", "127.0.0.1", 8888, "", ""},
		{"socks5://127.0.0.1", "socks5", "127.0.0.1", 1080, "", ""},
		{"http://user:secret@127.0.0.1:8080", "http", "127.0.0.1", 8080, "user", "secret"},
	}
	for _, c := range cases {
		pc, err := parseProxyURL(c.in)
		if err != nil {
			t.Fatalf("%q: 解析失败: %v", c.in, err)
		}
		if pc.Type != c.typ || pc.Server != c.server || pc.Port != c.port || pc.Username != c.user || pc.Password != c.pass {
			t.Fatalf("%q: got %+v, want type=%s server=%s port=%d user=%s pass=%s",
				c.in, pc, c.typ, c.server, c.port, c.user, c.pass)
		}
	}
}

func TestParseProxyURLErrors(t *testing.T) {
	for _, in := range []string{"", "://bad", "socks7://127.0.0.1:1080", "http://:8080", "http://127.0.0.1:99999"} {
		if pc, err := parseProxyURL(in); err == nil {
			t.Fatalf("%q: 期望报错，实际得到 %+v", in, pc)
		}
	}
}

func TestInlineProxyGroup(t *testing.T) {
	specs := []string{"socks5://127.0.0.1:7890", "http://127.0.0.1:8080"}
	g1 := inlineProxyGroup(specs)
	if g1 == nil || len(g1.Proxies) != 2 {
		t.Fatalf("应解析出 2 个代理: %+v", g1)
	}
	if g1.LoadBalance != "fastest" {
		t.Fatalf("内联代理组应按 fastest 选路: %q", g1.LoadBalance)
	}
	if g1.Stats == nil || g1.Stats.ProxyStats == nil {
		t.Fatal("内联代理组必须初始化 Stats，才能复用测速统计")
	}
	// 相同列表（含逗号串）应命中同一代理组（保留统计）
	g2 := inlineProxyGroup([]string{"socks5://127.0.0.1:7890,http://127.0.0.1:8080"})
	if g2 != g1 {
		t.Fatal("相同代理列表应命中缓存复用同一代理组")
	}
	// 全部无效 → nil（调用方回落域名规则/直连）
	if inlineProxyGroup([]string{"socks7://127.0.0.1:1", "://bad"}) != nil {
		t.Fatal("无效代理应返回 nil")
	}
	if inlineProxyGroup(nil) != nil {
		t.Fatal("空列表应返回 nil")
	}
}

func TestSplitProxyList(t *testing.T) {
	got := splitProxyList("socks5://a:1, http://b:2;https://c:3 socks5://a:1")
	want := []string{"socks5://a:1", "http://b:2", "https://c:3"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}
