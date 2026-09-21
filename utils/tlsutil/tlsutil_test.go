package tlsutil

import (
	"crypto/tls"
	"net"
	"os"
	"testing"
)

// 纯逻辑：显式列表必须包含静态 RSA 套件（Go 1.22+ 已从默认列表剔除），
// 且与 tls.CipherSuites() 合并后无重复。
func TestCipherSuitesWithRSAKex(t *testing.T) {
	suites := CipherSuitesWithRSAKex()
	if len(suites) == 0 {
		t.Fatal("套件列表为空")
	}
	seen := make(map[uint16]bool, len(suites))
	for _, id := range suites {
		if seen[id] {
			t.Fatalf("套件重复: 0x%04x", id)
		}
		seen[id] = true
	}
	for _, want := range []uint16{
		tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_RSA_WITH_AES_128_CBC_SHA,
		tls.TLS_RSA_WITH_AES_256_CBC_SHA,
	} {
		if !seen[want] {
			t.Fatalf("缺少静态 RSA 套件: 0x%04x", want)
		}
	}
	if !seen[tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256] {
		t.Fatal("缺少现代 ECDHE 套件")
	}
}

// 实网冒烟（env 门控）：对仅支持 TLS1.2 + 静态 RSA 的旧 CDN 做握手，
// 验证显式套件列表能建立连接。跳过条件：未设置 TVGATE_LIVE_TLS。
func TestLiveRSAKexHandshake(t *testing.T) {
	if os.Getenv("TVGATE_LIVE_TLS") == "" {
		t.Skip("需要 TVGATE_LIVE_TLS=1 启用实网握手验证")
	}
	host := os.Getenv("TVGATE_LIVE_TLS_HOST")
	if host == "" {
		host = "stream-shanghai-ct-61-172-246-231.edgesrv.com:443"
	}
	if _, err := net.LookupHost(host[:len(host)-4]); err != nil {
		t.Skipf("目标域名不可解析，跳过: %v", err)
	}
	conn, err := tls.Dial("tcp", host, &tls.Config{CipherSuites: CipherSuitesWithRSAKex()})
	if err != nil {
		t.Fatalf("旧 CDN 握手失败: %v", err)
	}
	defer conn.Close()
	cs := conn.ConnectionState().CipherSuite
	t.Logf("握手成功 cipher=0x%04x version=0x%04x", cs, conn.ConnectionState().Version)
}
