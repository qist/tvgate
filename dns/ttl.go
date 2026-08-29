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

// systemNameservers 读取系统 DNS 服务器（/etc/resolv.conf）
// resolv.conf 缺失/为空时兜底常见公共 DNS（安卓/精简容器无 resolv.conf 时）
func systemNameservers() []string {
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
	if len(out) == 0 {
		out = []string{"223.5.5.5", "119.29.29.29", "8.8.8.8"}
	}
	return out
}

// systemLookupTTL 向系统 DNS 服务器发原始 UDP 查询，返回 A/AAAA 记录及真实 TTL
func systemLookupTTL(ctx context.Context, host string, wantAAAA bool, timeout time.Duration) ([]DNSRecord, error) {
	qtype := dns.TypeA
	if wantAAAA {
		qtype = dns.TypeAAAA
	}
	msg := new(dns.Msg)
	msg.SetQuestion(dns.Fqdn(host), qtype)
	msg.SetEdns0(4096, false)

	var lastErr error
	for _, ns := range systemNameservers() {
		client := &dns.Client{Net: "udp", Timeout: timeout}
		resp, _, err := client.ExchangeContext(ctx, msg, net.JoinHostPort(ns, "53"))
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
