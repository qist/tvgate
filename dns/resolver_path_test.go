package dns

import (
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/qist/tvgate/config"
)

// fakeDNSServer 本地 UDP 假 DNS 服务器：记录收到的 A 查询，并返回固定 IP。
// 用于实证"配置的 resolver 是否真的被命中"。
type fakeDNSServer struct {
	mu    sync.Mutex
	asked []string // 收到的 question name 列表
	addr  string   // 监听地址 host:port
}

func startFakeDNSServer(t *testing.T, port int) *fakeDNSServer {
	t.Helper()
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
	if err != nil {
		t.Fatalf("listen fake dns: %v", err)
	}
	t.Cleanup(func() { pc.Close() })

	f := &fakeDNSServer{addr: pc.LocalAddr().(*net.UDPAddr).String()}
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := pc.ReadFromUDP(buf)
			if err != nil {
				return
			}
			req := new(dns.Msg)
			if req.Unpack(buf[:n]) != nil || len(req.Question) == 0 {
				continue
			}
			f.mu.Lock()
			for _, q := range req.Question {
				f.asked = append(f.asked, q.Name)
			}
			f.mu.Unlock()

			resp := new(dns.Msg)
			resp.SetReply(req)
			rr := &dns.A{
				Hdr: dns.RR_Header{
					Name:   req.Question[0].Name,
					Rrtype: dns.TypeA,
					Class:  dns.ClassINET,
					Ttl:    60,
				},
				A: net.ParseIP("1.2.3.4").To4(),
			}
			resp.Answer = append(resp.Answer, rr)
			if b, err := resp.Pack(); err == nil {
				pc.WriteToUDP(b, addr)
			}
		}
	}()
	return f
}

func (f *fakeDNSServer) askedNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.asked))
	copy(out, f.asked)
	return out
}

// cloneResolver 构造一个"仅含指定明文 DNS，且系统解析用正式实例"的 Resolver，
// 与生产走同一套代码路径。
func testResolver(server string, timeout time.Duration) *Resolver {
	if timeout == 0 {
		timeout = 2 * time.Second
	}
	return &Resolver{
		clients:        []dnsClient{&plainDNSClient{server: server, timeout: timeout}},
		timeout:        timeout,
		systemResolver: GetInstance().systemResolver,
	}
}

// TestConfiguredDNSPriorityAndUsed: 配置的 DNS 必走且最先命中。
// 用本地假 DNS 作为配置的 dns.servers，若它收到了该域名的查询，
// 则说明解析确实落到配置的 DNS（而非跳过它走系统）。
func TestConfiguredDNSPriorityAndUsed(t *testing.T) {
	port := 15353
	fake := startFakeDNSServer(t, port)

	r := testResolver("127.0.0.1:15353", 0)
	host := "fake-verify.example.com"

	ips, err := r.LookupIP(host)
	if err != nil {
		t.Fatalf("LookupIP err: %v", err)
	}
	// 若命中配置的 fake DNS，应返回其固定答案 1.2.3.4
	found := false
	for _, ip := range ips {
		if ip.String() == "1.2.3.4" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 1.2.3.4 from configured fake DNS, got %v", ips)
	}

	// 配置 DNS 确实收到了该查询 → 没有被跳过走向系统
	names := fake.askedNames()
	if len(names) == 0 {
		t.Fatalf("configured fake DNS received no query for %q; configured path was NOT hit", host)
	}
	for _, n := range names {
		t.Logf("configured fake DNS received query: %s", n)
	}
	match := false
	for _, n := range names {
		if strings.EqualFold(n, dns.Fqdn(host)) {
			match = true
		}
	}
	if !match {
		t.Fatalf("configured fake DNS got queries %v, but not for %s", names, host)
	}
}

// TestSystemFallbackWhenConfiguredDead: 配置的 DNS 不可达时，回落系统解析。
func TestSystemFallbackWhenConfiguredDead(t *testing.T) {
	// 127.0.0.1:1 无监听 → 必失败，逼出系统兜底
	r := testResolver("127.0.0.1:1", 1500*time.Millisecond)

	ips, err := r.LookupIP("www.baidu.com")
	if err != nil {
		t.Fatalf("expected system fallback to resolve, got err: %v", err)
	}
	if len(ips) == 0 {
		t.Fatalf("system fallback returned no IPs")
	}
	t.Logf("system fallback resolved %d IPs, first=%v", len(ips), ips[0])
}

// TestDNSConfigTakesEffect: 验证 config.yaml 里的 dns.servers 配置真实生效。
// 把本地假 DNS 写进配置，走与生产一致的 loadConfig 加载路径，端到端断言：
// 配置被读入、客户端被构建、解析确实命中配置的那台 DNS。
func TestDNSConfigTakesEffect(t *testing.T) {
	const fakeSrv = "127.0.0.1:15353"
	fake := startFakeDNSServer(t, 15353)

	// 临时设置 DNS 配置，并保证测试后恢复，避免污染全局 config.Cfg
	oldDNS := config.Cfg.DNS
	config.Cfg.DNS = config.DNSConfig{
		Servers:  []string{fakeSrv},
		Timeout:  2 * time.Second,
		MaxConns: 10,
	}
	defer func() { config.Cfg.DNS = oldDNS }()

	// 走真实加载路径（loadConfig 从 config.Cfg.DNS 生成 resolver/clients）
	r := &Resolver{}
	r.loadConfig()

	// 1) 配置被读入：GetResolvers 返回配置的服务器
	gotResolvers := r.GetResolvers()
	if len(gotResolvers) != 1 || gotResolvers[0] != fakeSrv {
		t.Fatalf("GetResolvers() = %v, want [%s]（DNS 配置未被读取）", gotResolvers, fakeSrv)
	}

	// 2) 客户端已按配置构建
	r.mutex.RLock()
	nCli := len(r.clients)
	r.mutex.RUnlock()
	if nCli == 0 {
		t.Fatal("loadConfig 未基于 dns.servers 构建 DNS 客户端")
	}

	// 3) 端到端：解析走配置的 DNS，应拿到假 DNS 的固定应答 1.2.3.4
	host := "config-effective.example.com"
	ips, err := r.LookupIP(host)
	if err != nil {
		t.Fatalf("LookupIP err: %v", err)
	}
	found := false
	for _, ip := range ips {
		if ip.String() == "1.2.3.4" {
			found = true
		}
	}
	if !found {
		t.Fatalf("配置的 DNS 未生效（未命中假 DNS 的 1.2.3.4），got %v", ips)
	}

	// 4) 反向确认：配置的 DNS 确实收到了该查询
	if got := fake.askedNames(); len(got) == 0 {
		t.Fatal("配置的 DNS 未收到任何查询（配置未生效）")
	} else {
		t.Logf("配置的 DNS 收到查询: %v", got)
	}
}
