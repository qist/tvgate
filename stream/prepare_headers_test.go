package stream

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestPrepareProxyHeadersTSKeepsContentLengthWhenCacheOff：TS 缓存关闭时是 1:1 原样转发，
// 上游 Content-Length 与实际下发字节一致，应保留为定长响应（200 + 定长），
// 与旧代理一致——部分播放器不认 chunked，或依赖 Content-Length 判完整/进度。
func TestPrepareProxyHeadersTSKeepsContentLengthWhenCacheOff(t *testing.T) {
	old := GlobalTSCache
	GlobalTSCache = nil // 关闭 TS 缓存（流特性总开关）
	defer func() { GlobalTSCache = old }()

	resp := &http.Response{Header: http.Header{"Content-Length": []string{"724944"}}}
	w := httptest.NewRecorder()
	w.Header().Set("Content-Length", "724944")

	PrepareProxyHeaders(w, resp, "/https:/hoytv-live-stream.hoy.tv/ch76/seg_1.ts")

	if got := w.Header().Get("Content-Length"); got != "724944" {
		t.Fatalf("TS 缓存关闭时应保留 Content-Length，实际 %q", got)
	}
}

// TestPrepareProxyHeadersTSRemovesContentLengthWhenCacheOn：TS 缓存开启时正文会被关键帧
// 对齐裁剪、长度可能变短，必须移除 Content-Length，否则客户端会等满/报错。
func TestPrepareProxyHeadersTSRemovesContentLengthWhenCacheOn(t *testing.T) {
	old := GlobalTSCache
	GlobalTSCache = &TSCache{} // 非 nil 即视为开启
	defer func() { GlobalTSCache = old }()

	resp := &http.Response{Header: http.Header{"Content-Length": []string{"724944"}}}
	w := httptest.NewRecorder()
	w.Header().Set("Content-Length", "724944")

	PrepareProxyHeaders(w, resp, "/https:/hoytv-live-stream.hoy.tv/ch76/seg_1.ts")

	if got := w.Header().Get("Content-Length"); got != "" {
		t.Fatalf("TS 缓存开启时应移除 Content-Length，实际 %q", got)
	}
}

// TestPrepareProxyHeadersLiveStreamRemovesContentLength：直播流长度不定，始终走 chunked。
func TestPrepareProxyHeadersLiveStreamRemovesContentLength(t *testing.T) {
	old := GlobalTSCache
	GlobalTSCache = nil
	defer func() { GlobalTSCache = old }()

	resp := &http.Response{Header: http.Header{"Content-Length": []string{"100"}}}
	w := httptest.NewRecorder()
	w.Header().Set("Content-Length", "100")

	PrepareProxyHeaders(w, resp, "/live/channel.flv")

	if got := w.Header().Get("Content-Length"); got != "" {
		t.Fatalf("直播流应移除 Content-Length，实际 %q", got)
	}
}
