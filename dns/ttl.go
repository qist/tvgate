package dns

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/miekg/dns"
)

// DNSRecord 一条 DNS 应答记录（含 TTL）
type DNSRecord struct {
	IP  net.IP
	TTL uint32
}

// resolvConfNameservers 读取 /etc/resolv.conf 中的 nameserver
func resolvConfNameservers() []string {
	var out []string
	f, err := os.Open("/etc/resolv.conf")
	if err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fields := strings.Fields(strings.TrimSpace(sc.Text()))
			if len(fields) >= 2 && fields[0] == "nameserver" {
				if ip := net.ParseIP(fields[1]); ip != nil {
					out = append(out, fields[1])
				}
			}
		}
	}
	return out
}

// plainDNSServerHost 从配置的 dns.servers 条目中提取可直连的明文 DNS 地址（host 或 host:port）
// dnscrypt stamp / DoH / DoT / DoH3 等非明文协议返回空（走客户端机制，无法取 TTL）
func plainDNSServerHost(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "sdns://") || strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "tls://") || strings.HasPrefix(s, "quic://") ||
		strings.HasPrefix(s, "h3://") {
		return ""
	}
	if h, p, err := net.SplitHostPort(s); err == nil {
		if net.ParseIP(h) != nil {
			return net.JoinHostPort(h, p)
		}
		return ""
	}
	if net.ParseIP(s) != nil {
		return s
	}
	return ""
}

// nameserversForTTL 返回 TTL 原始查询使用的 DNS 服务器列表（按优先级，去重）：
// 1. YAML 配置 dns.servers 中的明文服务器（安卓/内网优先，可指向内网 DNS，避免外部公共 DNS 解析不了内网域名）
// 2. /etc/resolv.conf
// 3. 公共 DNS 兜底（仅前两者都拿不到时）
func nameserversForTTL() []string {
	var out []string
	seen := map[string]bool{}
	add := func(ns string) {
		if ns == "" || seen[ns] {
			return
		}
		seen[ns] = true
		out = append(out, ns)
	}
	for _, s := range GetInstance().GetResolvers() {
		add(plainDNSServerHost(s))
	}
	for _, ns := range resolvConfNameservers() {
		add(ns)
	}
	if len(out) == 0 {
		for _, ns := range []string{"223.5.5.5", "119.29.29.29", "8.8.8.8"} {
			add(ns)
		}
	}
	return out
}

// systemLookupTTL 向 DNS 服务器发原始 UDP 查询，返回 A/AAAA 记录及真实 TTL
func systemLookupTTL(ctx context.Context, host string, wantAAAA bool, timeout time.Duration) ([]DNSRecord, error) {
	qtype := dns.TypeA
	if wantAAAA {
		qtype = dns.TypeAAAA
	}
	msg := new(dns.Msg)
	msg.SetQuestion(dns.Fqdn(host), qtype)
	msg.SetEdns0(4096, false)

	var lastErr error
	for _, ns := range nameserversForTTL() {
		addr := ns
		if _, _, err := net.SplitHostPort(ns); err != nil {
			addr = net.JoinHostPort(ns, "53")
		}
		client := &dns.Client{Net: "udp", Timeout: timeout}
		resp, _, err := client.ExchangeContext(ctx, msg, addr)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.Rcode != dns.RcodeSuccess {
			lastErr = fmt.Errorf("dns: rcode %d", resp.Rcode)
			continue
		}
		var out []DNSRecord
		for _, ans := range resp.Answer {
			switch rr := ans.(type) {
			case *dns.A:
				if !wantAAAA {
					out = append(out, DNSRecord{IP: rr.A, TTL: rr.Hdr.Ttl})
				}
			case *dns.AAAA:
				if wantAAAA {
					out = append(out, DNSRecord{IP: rr.AAAA, TTL: rr.Hdr.Ttl})
				}
			}
		}
		return out, nil
	}
	return nil, lastErr
}
