package http

import (
	nethttp "net/http"
	"testing"

	"github.com/qist/tvgate/config"
)

// 回归：配置里的 *bool 字段（insecure_skip_verify / disable_keepalives）为 nil 时创建 client
// 不能再空指针 panic。实测事故：后台保存配置 → config/load 发布了一份还没补默认值的全局配置
// （HTTP 的 nil 指针）→ 出口通知让播放器重载 → 其 EPG 拉取的 goroutine 走 NewHTTPClient
// 在 newTransport 里解引用 nil，直接 panic 把进程带走。
func TestNewTransportNilPointerSafe(t *testing.T) {
	client := NewHTTPClient(&config.Config{}, nil) // HTTP 子结构全为 nil / 零值
	if client == nil || client.Transport == nil {
		t.Fatal("空配置也应能建出 client")
	}
	tr, ok := client.Transport.(*nethttp.Transport)
	if !ok {
		t.Fatalf("transport 类型不对: %T", client.Transport)
	}
	if tr.DisableKeepAlives {
		t.Fatal("nil 时应回退 false（保持长连接）")
	}
	if tr.TLSClientConfig == nil || tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("nil 时应回退 false（保持 TLS 校验）")
	}
}

func TestBoolOr(t *testing.T) {
	yes, no := true, false
	if !boolOr(nil, true) || boolOr(nil, false) {
		t.Fatal("nil 应取 fallback")
	}
	if !boolOr(&yes, false) {
		t.Fatal("非 nil 应取指针值 true")
	}
	if boolOr(&no, true) {
		t.Fatal("非 nil 应取指针值 false")
	}
}
