package player

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qist/tvgate/config"
)

// 回归：订阅 URL 带轮换 token / 被编辑时，频道 key 必须随「名称+分组」保持稳定，
// 而不是随 URL 全量轮换（否则分享深链失效、播放中频道中途消失）。
func TestStableKeyAcrossURLChange(t *testing.T) {
	b := false
	config.Cfg.HTTP.InsecureSkipVerify = &b
	config.Cfg.HTTP.DisableKeepAlives = &b

	bodyV1 := "上海联通,#genre#\n北京卫视4K,http://src/a/468105442.smil/01.m3u8?token=111\n"
	bodyV2 := "上海联通,#genre#\n北京卫视4K,http://src/b/468105442.smil/index.m3u8?GuardEncType=2&accountinfo=zzz\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(bodyV1))
	}))
	defer srv.Close()

	setTestPlayer(config.PlayerConfig{Enabled: true, Subscription: srv.URL}, t)
	mgr := NewManager(&config.Cfg.Player)
	mgr.httpClient = srv.Client()
	mgr.Reload()

	chans := mgr.Channels()
	if len(chans) != 1 {
		t.Fatalf("期望 1 频道, got %d", len(chans))
	}
	keyV1 := chans[0].Key
	if keyV1 == "" {
		t.Fatal("key 为空")
	}

	// URL 文本变化（换源/token 轮换），名称+分组不变
	bodyV1 = bodyV2
	mgr.Reload()

	chans2 := mgr.Channels()
	if len(chans2) != 1 {
		t.Fatalf("重载后期望 1 频道, got %d", len(chans2))
	}
	if got := chans2[0].Key; got != keyV1 {
		t.Fatalf("URL 变化后 key 应稳定: v1=%s v2=%s", keyV1, got)
	}

	// 同名同组双源：第一路用身份 key，第二路按 URL 派生且互不相同
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	if len(mgr.order) != 1 {
		t.Fatalf("内部频道数异常: %d", len(mgr.order))
	}
}

// 同名同组的多源条目：第一路身份 key，第二路 URL key，二者不同。
func TestStableKeyDuplicateNameURLFallback(t *testing.T) {
	b := false
	config.Cfg.HTTP.InsecureSkipVerify = &b
	config.Cfg.HTTP.DisableKeepAlives = &b

	content := "上海联通,#genre#\n" +
		"北京卫视4K,http://src/a/index.m3u8\n" +
		"北京卫视4K,http://src/b/index.m3u8\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(content))
	}))
	defer srv.Close()

	setTestPlayer(config.PlayerConfig{Enabled: true, Subscription: srv.URL}, t)
	mgr := NewManager(&config.Cfg.Player)
	mgr.httpClient = srv.Client()
	mgr.Reload()

	chans := mgr.Channels()
	if len(chans) != 2 {
		t.Fatalf("期望 2 频道, got %d", len(chans))
	}
	if chans[0].Key == chans[1].Key {
		t.Fatalf("同名双源 key 不应相同: %s", chans[0].Key)
	}
}
