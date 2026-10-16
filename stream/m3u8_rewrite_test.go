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
	// 内嵌 URI 应被重写为本站代理地址（保留源地址作为路径）
	if !strings.Contains(got, `URI="http://154.219.96.221:8080/https://ch77-live-stream.hoy.tv/key/static_key.key"`) {
		t.Fatalf("EXT-X-KEY 的 URI 未套代理前缀:\n%s", got)
	}
	// 分片行同样走代理
	if !strings.Contains(got, "http://154.219.96.221:8080/https://cdn.example.com/seg/1.ts") {
		t.Fatalf("分片行未套代理前缀:\n%s", got)
	}
	// IV/KEYFORMAT 等其它属性保持原样
	if !strings.Contains(got, `IV=0x0000000000000000000000000007D137`) {
		t.Fatalf("EXT-X-KEY 其它属性被破坏:\n%s", got)
	}
}
