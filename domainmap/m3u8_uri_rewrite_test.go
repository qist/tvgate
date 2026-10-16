package domainmap

import (
	"strings"
	"testing"
)

// TestReplaceSpecialNestedURLCleanRewritesEmbeddedURI：m3u8 内嵌 URI（EXT-X-KEY/EXT-X-MAP 等）
// 必须与分片行一样改写成前端 source host，否则客户端会直连上游源站（未经域名映射、泄露源地址）。
func TestReplaceSpecialNestedURLCleanRewritesEmbeddedURI(t *testing.T) {
	dm := &DomainMapper{}
	seen := map[string]struct{}{}
	frontendScheme := "http"
	frontendHost := "iptv.example.com:8080"
	sourceHost := frontendHost

	// EXT-X-KEY 的绝对 URI：应改写为前端 host
	line := `#EXT-X-KEY:METHOD=AES-128,URI="https://ch77-live-stream.hoy.tv/key/static_key.key",IV=0x0000000000000000000000000007D137,KEYFORMAT="identity",KEYFORMATVERSIONS="1"`
	got := string(dm.replaceSpecialNestedURLClean(line, frontendScheme, frontendHost, sourceHost, nil, "token", seen))
	want := `URI="http://iptv.example.com:8080/key/static_key.key"`
	if !strings.Contains(got, want) {
		t.Fatalf("EXT-X-KEY 的 URI 未改写为前端 host:\ngot:  %s\nwant contains: %s", got, want)
	}
	// 其它属性原样保留
	if !strings.Contains(got, "IV=0x0000000000000000000000000007D137") || !strings.Contains(got, `KEYFORMAT="identity"`) {
		t.Fatalf("EXT-X-KEY 其它属性被破坏:\n%s", got)
	}

	// EXT-X-MAP 的绝对 URI：同样改写，且 query 不被 percent-encode
	line2 := `#EXT-X-MAP:URI="https://cdn.example.com/init.mp4?token=abc%3D&x=1"`
	got2 := string(dm.replaceSpecialNestedURLClean(line2, frontendScheme, frontendHost, sourceHost, nil, "token", seen))
	want2 := `URI="http://iptv.example.com:8080/init.mp4?token=abc%3D&x=1"`
	if !strings.Contains(got2, want2) {
		t.Fatalf("EXT-X-MAP 的 URI 未正确改写:\ngot:  %s\nwant contains: %s", got2, want2)
	}
}

// TestReplaceSpecialNestedURLCleanKeepsRelativeURI：相对 URI 不应被改写为绝对前端地址。
func TestReplaceSpecialNestedURLCleanKeepsRelativeURI(t *testing.T) {
	dm := &DomainMapper{}
	seen := map[string]struct{}{}
	line := `#EXT-X-KEY:METHOD=AES-128,URI="key/static_key.key"`
	got := string(dm.replaceSpecialNestedURLClean(line, "http", "iptv.example.com:8080", "iptv.example.com:8080", nil, "token", seen))
	if !strings.Contains(got, `URI="key/static_key.key"`) {
		t.Fatalf("相对 URI 不应被改写:\n%s", got)
	}
}
