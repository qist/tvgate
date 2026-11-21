package config

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestBuildPoolNodesMultiServerAuto(t *testing.T) {
	g := &ProxyGroupConfig{
		Resolve: "local",
		Pool: &ProxyPoolConfig{
			Servers:  []string{"192.0.2.1:1000-1001", "socks5://user:pass@192.0.2.2:2000"},
			Protocol: "auto",
		},
	}
	g.Pool.SetDefaults()

	nodes, err := g.BuildPoolNodes()
	if err != nil {
		t.Fatalf("BuildPoolNodes err: %v", err)
	}
	// 第一段 2 个端口 × auto 2 种协议 = 4；第二个显式 socks5 = 1
	if len(nodes) != 5 {
		t.Fatalf("nodes = %d, want 5: %+v", len(nodes), nodes)
	}
	for _, n := range nodes {
		if n.Resolve != "local" {
			t.Fatalf("节点 %s 未继承 local resolve: %q", n.Name, n.Resolve)
		}
	}
	if nodes[0].Username != "" || nodes[0].Password != "" {
		t.Fatalf("无认证节点不应带账号: %+v", nodes[0])
	}
	if nodes[4].Username != "user" || nodes[4].Password != "pass" {
		t.Fatalf("显式 scheme 节点的认证信息丢失: %+v", nodes[4])
	}
}

func TestPoolValidateRejectsBadInput(t *testing.T) {
	cases := []*ProxyPoolConfig{
		{Servers: []string{"1.2.3.4"}},
		{Servers: []string{"1.2.3.4:70000"}},
		{Servers: []string{"1.2.3.4:100-99"}},
		{Servers: []string{"1.2.3.4:1-2"}, Protocol: "ftp"},
		{Servers: []string{"1.2.3.4:1-2"}, ProbeURL: "ftp://example.com"},
	}
	for _, c := range cases {
		if err := c.Validate(); err == nil {
			t.Fatalf("应当校验失败: %+v", c)
		}
	}
}

func TestPoolDefaults(t *testing.T) {
	p := &ProxyPoolConfig{}
	p.SetDefaults()
	if p.Protocol != "auto" {
		t.Fatalf("protocol = %q, want auto", p.Protocol)
	}
	if p.ProbeEnabled == nil || !*p.ProbeEnabled {
		t.Fatal("probe_enabled 默认应为 true")
	}
	if p.ProbeInterval != 60*time.Second || p.ProbeTimeout != 2*time.Second {
		t.Fatalf("探测默认值异常: %+v", p)
	}
	if p.ProbeConcurrency != 16 || p.TopN != 30 || p.MaxPorts != 2048 {
		t.Fatalf("数量默认值异常: %+v", p)
	}
	if p.ProbeRetries != 0 {
		t.Fatalf("probe_retries 默认应为 0: %d", p.ProbeRetries)
	}
	p.ProbeRetries = -1
	p.SetDefaults()
	if p.ProbeRetries != 0 {
		t.Fatalf("负值应归零: %d", p.ProbeRetries)
	}
	p.ProbeRetries = 99
	p.SetDefaults()
	if p.ProbeRetries != 5 {
		t.Fatalf("上限应为 5: %d", p.ProbeRetries)
	}
}

func TestActiveProxiesIncludesStaticAndPool(t *testing.T) {
	g := &ProxyGroupConfig{
		Proxies: []*ProxyConfig{{Name: "static"}},
		Pool:    &ProxyPoolConfig{Servers: []string{"192.0.2.1:1"}},
	}
	poolNodes := []*ProxyConfig{{Name: "pool-1"}}
	g.SetPoolNodes(poolNodes)
	got := g.ActiveProxies()
	if len(got) != 2 || got[0].Name != "static" || got[1].Name != "pool-1" {
		t.Fatalf("ActiveProxies = %+v", got)
	}
	if nodes := g.PoolNodes(); len(nodes) != 1 || nodes[0].Name != "pool-1" {
		t.Fatalf("PoolNodes = %+v", nodes)
	}
	g.ClearPoolNodes()
	if len(g.ActiveProxies()) != 1 {
		t.Fatalf("清除池节点后应只剩静态代理: %+v", g.ActiveProxies())
	}
	if nodes := g.PoolNodes(); len(nodes) != 0 {
		t.Fatalf("清除后 PoolNodes 应为空: %+v", nodes)
	}
}

func TestProxyPoolYAMLWithoutProxies(t *testing.T) {
	const raw = `
proxygroups:
  hn-pool:
    domains: [tvgslb.hn.chinamobile.com]
    resolve: local
    pool:
      servers:
        - 221.229.214.145:20237-20239
      protocol: auto
      probe_enabled: false
      probe_interval: 30s
      probe_url: http://tvgslb.hn.chinamobile.com/main.m3u8
      probe_timeout: 1s
      probe_retries: 3
      probe_concurrency: 8
      top_n: 10
      fail_cooldown: 20s
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	cfg.SetDefaults()
	g := cfg.ProxyGroups["hn-pool"]
	if g == nil {
		t.Fatal("代理组未解析")
	}
	if g.Proxies != nil {
		t.Fatalf("pool 模式下 proxies 应保持 nil: %+v", g.Proxies)
	}
	if g.Resolve != "local" {
		t.Fatalf("resolve = %q", g.Resolve)
	}
	if g.Pool.ProbeEnabled == nil || *g.Pool.ProbeEnabled {
		t.Fatal("probe_enabled=false 未生效")
	}
	if g.Pool.ProbeInterval != 30*time.Second || g.Pool.ProbeTimeout != time.Second {
		t.Fatalf("探测时间解析错误: %+v", g.Pool)
	}
	if g.Pool.ProbeRetries != 3 {
		t.Fatalf("probe_retries = %d, want 3", g.Pool.ProbeRetries)
	}
	nodes, err := g.BuildPoolNodes()
	if err != nil {
		t.Fatalf("BuildPoolNodes: %v", err)
	}
	// 3 个端口 × auto 2 种协议
	if len(nodes) != 6 {
		t.Fatalf("nodes = %d, want 6", len(nodes))
	}
}

func TestPoolKeyStableWithDefaults(t *testing.T) {
	a := &ProxyGroupConfig{
		Resolve: "remote",
		Pool:    &ProxyPoolConfig{Servers: []string{"192.0.2.1:1-2"}},
	}
	b := &ProxyGroupConfig{
		Pool: &ProxyPoolConfig{Servers: []string{"192.0.2.1:1-2"}},
	}
	b.Pool.SetDefaults()
	// b 未显式设置 Resolve，PoolKey 内部按 remote 归一化
	if a.PoolKey() != b.PoolKey() {
		t.Fatalf("默认值与显式值应生成相同 PoolKey:\n%s\n%s", a.PoolKey(), b.PoolKey())
	}
}
