package stream

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHandleSpecialContentRewritesEmbeddedURI：m3u8 内嵌 URI（EXT-X-KEY/EXT-X-MAP 等）
// 必须与分片行一样套上本站代理前缀，否则客户端会直连源站（key 未经代理、泄露源地址）。
func TestHandleSpecialContentRewritesEmbeddedURI(t *testing.T) {
	body := strings.Join([]string{
		"#EXTM3U",
		"#EXT-X-VERSION:3",
		`#EXT-X-KEY:METHOD=AES-128,URI="https://ch77-live-stream.hoy.tv/key/static_key.key",IV=0x0000000000000000000000000007D137,KEYFORMAT="identity",KEYFORMATVERSIONS="1"`,
		"#EXTINF:6.000,",
		"https://cdn.example.com/seg/1.ts",
		"",
	}, "\n")

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.apple.mpegurl"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
	}
	r := httptest.NewRequest(http.MethodGet, "/https:/hoytv-live-stream.hoy.tv/ch76/index-fhd.m3u8", nil)
	r.Host = "154.219.96.221:8080"
	w := httptest.NewRecorder()

	handleSpecialContent(w, r, resp, nil)

	got := w.Body.String()
	// 内嵌 URI 应被重写为本站代理地址（保留源地址作为路径）。
	// 必须是单斜杠 /https:/ 形式：双斜杠会被路由层 cleanPath 307 跳转，
	// 不跟随重定向的播放器（部分安卓 ijk 内核）会把跳转页当成 AES key，画面出不来。
	if !strings.Contains(got, `URI="http://154.219.96.221:8080/https:/ch77-live-stream.hoy.tv/key/static_key.key"`) {
		t.Fatalf("EXT-X-KEY 的 URI 未套代理前缀（单斜杠形式）:\n%s", got)
	}
	if strings.Contains(got, "8080/https://") {
		t.Fatalf("改写结果仍是双斜杠 /https:// 形式，会触发 307 跳转:\n%s", got)
	}
	// 分片行同样走代理
	if !strings.Contains(got, "http://154.219.96.221:8080/https:/cdn.example.com/seg/1.ts") {
		t.Fatalf("分片行未套代理前缀:\n%s", got)
	}
	// IV/KEYFORMAT 等其它属性保持原样
	if !strings.Contains(got, `IV=0x0000000000000000000000000007D137`) {
		t.Fatalf("EXT-X-KEY 其它属性被破坏:\n%s", got)
	}
}

// TestHandleSpecialContentNormalizesPartialContent：播放列表正文被重写后长度与源站不再一致。
// 上游对 "Range: bytes=0-" 常回 206 + Content-Range，必须归一为 200 全量并清掉范围头，
// 否则严格遵守 Content-Range 的客户端（ffmpeg / ijk 等）只读到源站长度、把重写后的正文
// 截断在分片名中间 → parse_playlist error / 刷新列表失败 → 播放几秒即停。
func TestHandleSpecialContentNormalizesPartialContent(t *testing.T) {
	body := strings.Join([]string{
		"#EXTM3U",
		"#EXT-X-TARGETDURATION:4",
		`#EXT-X-KEY:METHOD=AES-128,URI="https://ch77-live-stream.hoy.tv/key/static_key.key",IV=0x0000000000000000000000000007EE2E`,
		"#EXTINF:3.98933,",
		"index-fhd-5-a1_520157.aac",
		"",
	}, "\n")

	resp := &http.Response{
		StatusCode: http.StatusPartialContent, // 源站对 Range 请求回的 206
		Header: http.Header{
			"Content-Type":   []string{"application/vnd.apple.mpegurl"},
			"Content-Range":  []string{"bytes 0-2147/2148"},
			"Accept-Ranges":  []string{"bytes"},
			"Content-Length": []string{"2148"},
		},
		Body: io.NopCloser(bytes.NewReader([]byte(body))),
	}
	r := httptest.NewRequest(http.MethodGet, "/https:/hoytv-live-stream.hoy.tv/ch76/index-fhd-5-a1.m3u8", nil)
	r.Host = "154.219.96.221:8080"
	w := httptest.NewRecorder()

	handleSpecialContent(w, r, resp, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("206 未归一为 200，实际状态码 %d", w.Code)
	}
	if cr := w.Header().Get("Content-Range"); cr != "" {
		t.Fatalf("Content-Range 未清除：%q", cr)
	}
	if ar := w.Header().Get("Accept-Ranges"); ar != "" {
		t.Fatalf("Accept-Ranges 未清除：%q", ar)
	}
	if cl := w.Header().Get("Content-Length"); cl != "" {
		t.Fatalf("Content-Length 未清除：%q", cl)
	}
}
