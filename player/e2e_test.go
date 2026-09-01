package player

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qist/tvgate/config"
)

// newTServer 按 path 返回(contentType, body) 的假上游。
func newTServer(h func(path string) (string, string)) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ct, body := h(r.URL.Path)
		w.Header().Set("Content-Type", ct)
		w.Write([]byte(body))
	}))
}

// req 执行请求并返回 recorder。
func req(h *Handler, method, path, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	rr := httptest.NewRecorder()
	switch {
	case strings.HasPrefix(path, "/api/player/channels"):
		h.ServeChannels(rr, req)
	case strings.HasPrefix(path, "/api/player/epg"):
		h.ServeEPG(rr, req)
	default:
		h.ServePull(rr, req)
	}
	return rr
}

// TestEndToEndPull 运行级冒烟：订阅(带 http HLS 源) → /api/player/channels → /player/<key> 拉 m3u8 → 子分片回拉。
func TestEndToEndPull(t *testing.T) {
	b := false
	config.Cfg.HTTP.InsecureSkipVerify = &b
	config.Cfg.HTTP.DisableKeepAlives = &b

	// 上游：master.m3u8 + 相对分片
	upstream := newTServer(func(p string) (string, string) {
		switch p {
		case "/live/master.m3u8":
			return "application/vnd.apple.mpegurl", "#EXTM3U\n#EXTINF:5.0,\nseg1.ts\n#EXT-X-ENDLIST\n"
		case "/live/seg1.ts":
			return "video/mp2t", "TSBYTES-0123456789abcdef"
		default:
			return "text/plain", "404"
		}
	})
	defer upstream.Close()

	sub := newTServer(func(p string) (string, string) {
		return "text/plain", "央视,#genre#\nCCTV1," + upstream.URL + "/live/master.m3u8\n"
	})
	defer sub.Close()

	setTestPlayer(config.PlayerConfig{Enabled: true, Subscription: sub.URL}, t)

	mgr := NewManager(&config.Cfg.Player)
	mgr.httpClient = sub.Client()
	mgr.Reload()

	chans := mgr.Channels()
	if len(chans) != 1 {
		t.Fatalf("期望 1 频道, got %d", len(chans))
	}
	key := chans[0].Key
	if key == "" {
		t.Fatal("key 为空")
	}
	if strings.Contains(key, "://") {
		t.Fatalf("key 不应含源地址: %s", key)
	}

	h := NewHandler(mgr)
	h.httpClient = upstream.Client()
	h.stream = upstream.Client()

	// /player/<key> → 返回重写后的 m3u8（每行 /player/KEY/<token>，不暴露源地址）
	rr := req(h, "GET", "/player/"+key, "")
	if rr.Code != 200 {
		t.Fatalf("拉 m3u8 状态 %d body=%s", rr.Code, rr.Body.String())
	}
	m3 := rr.Body.String()
	if strings.Contains(m3, "://") || strings.Contains(m3, ".ts") {
		t.Fatalf("m3u8 不应暴露源地址（应为短 token）:\n%s", m3)
	}
	var tok string
	prefix := "/player/" + key + "/"
	for _, line := range strings.Split(m3, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			tok = strings.TrimPrefix(line, prefix)
			break
		}
	}
	if tok == "" {
		t.Fatalf("未取到分片 token: %s", m3)
	}
	// 子分片回拉（用短 token）→ 命中上游 seg1.ts
	rr2 := req(h, "GET", "/player/"+key+"/"+tok, "")
	if rr2.Code != 200 {
		t.Fatalf("子分片状态 %d body=%s", rr2.Code, rr2.Body.String())
	}
	if !strings.Contains(rr2.Body.String(), "TSBYTES") {
		t.Fatalf("子分片内容不对: %s", rr2.Body.String())
	}
}
