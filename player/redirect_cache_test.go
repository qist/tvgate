package player

import (
	"testing"
	"time"
)

// TestRedirectCacheExpiry 覆盖"一直播的频道也必须定期重新解析直链"。
// 只靠活跃窗口（lastUsed）时，只要频道一直在播，缓存会永远复用第一次解析出的地址；
// 而直播直链会过期，过期后 CDN 反复截断连接，播放器就表现为"播几秒断一次、
// 一直重连同一个坏地址"（上游看到的就是持续重连）。
func TestRedirectCacheExpiry(t *testing.T) {
	h := &Handler{}

	h.storeRedirect("fresh", "http://cdn/live.flv?sig=new")
	if got := h.getRedirect("fresh"); got != "http://cdn/live.flv?sig=new" {
		t.Fatalf("刚写入应命中缓存，得到 %q", got)
	}

	// 刚刚用过（活跃窗口内），但地址本身已超绝对有效期 → 必须判失效、重新解析
	h.redirects.Store("stale", &redirectCache{
		finalURL: "http://cdn/live.flv?sig=stale",
		lastUsed: time.Now(),
		storedAt: time.Now().Add(-redirectResolveTTL - time.Second),
	})
	if got := h.getRedirect("stale"); got != "" {
		t.Fatalf("直链超绝对有效期应判失效，却返回 %q", got)
	}

	// 活跃窗口超时同样失效（原有行为保持不变）
	h.redirects.Store("idle", &redirectCache{
		finalURL: "http://cdn/live.flv?sig=idle",
		lastUsed: time.Now().Add(-redirectActiveWindow - time.Second),
		storedAt: time.Now(),
	})
	if got := h.getRedirect("idle"); got != "" {
		t.Fatalf("久未使用应判失效，却返回 %q", got)
	}
}

// TestIsLiveFLVPath 确保"过早结束则清缓存"只作用在 FLV 直播直链上：
// m3u8 分片等短连接不能被误判（那会把正在用的解析结果清掉）。
func TestIsLiveFLVPath(t *testing.T) {
	for _, s := range []string{"http://h/live/1.flv?a=1", "https://h/a/b.FLV", "http://h/x.flv"} {
		if !isLiveFLVPath(s) {
			t.Fatalf("%s 应判为 FLV 直播直链", s)
		}
	}
	for _, s := range []string{"http://h/a.ts", "http://h/a.m3u8", "http://h/x.php?u=a.flv"} {
		if isLiveFLVPath(s) {
			t.Fatalf("%s 不应判为 FLV 直播直链", s)
		}
	}
}
