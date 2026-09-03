package cache

import (
	"testing"

	"github.com/qist/tvgate/config"
)

func TestAddRedirectIPChainCap(t *testing.T) {
	config.RedirectCache.Mapping = make(map[string]*config.RedirectChain)
	// 灌入 maxChainLen+10 个跳转目标
	for i := 0; i < maxChainLen+10; i++ {
		AddRedirectIP("origin.example.com", ip(i))
	}
	cd := config.RedirectCache.Mapping["origin.example.com"]
	if cd == nil {
		t.Fatal("chain missing")
	}
	if cd.ChainHead > maxChainLen {
		t.Fatalf("chain head %d exceeds cap %d", cd.ChainHead, maxChainLen)
	}
	if len(cd.Chain) != cd.ChainHead {
		t.Fatalf("sparse chain: len=%d head=%d", len(cd.Chain), cd.ChainHead)
	}
	// 旧的最先被淘汰：level 1 应为最近窗口的早期 IP
	if got := cd.Chain[1]; got != ip(10) {
		t.Fatalf("oldest retained = %s, want %s", got, ip(10))
	}
	if got := cd.Chain[cd.ChainHead]; got != ip(maxChainLen+9) {
		t.Fatalf("newest = %s, want %s", got, ip(maxChainLen+9))
	}
}

func TestAddRedirectIPMergeNewOnly(t *testing.T) {
	config.RedirectCache.Mapping = make(map[string]*config.RedirectChain)
	AddRedirectIP("a.example.com", "10.0.0.1")
	// 目标 10.0.0.2 自己作为 origin 的链：10.0.0.2 -> 10.0.0.3
	AddRedirectIP("10.0.0.2", "10.0.0.3")
	// a 记录 a -> 10.0.0.2 时，应把 10.0.0.2 自身链上的新 IP（10.0.0.3）并入 a
	AddRedirectIP("a.example.com", "10.0.0.2")
	a := config.RedirectCache.Mapping["a.example.com"]
	got := map[string]bool{}
	for _, ip := range a.Chain {
		got[ip] = true
	}
	for _, want := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"} {
		if !got[want] {
			t.Fatalf("a chain missing %s: %v", want, a.Chain)
		}
	}
	if len(a.Chain) != 3 {
		t.Fatalf("a chain has duplicates: %v", a.Chain)
	}
}

func ip(i int) string {
	return "10.1.0." + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
