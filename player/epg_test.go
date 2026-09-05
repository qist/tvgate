package player

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qist/tvgate/config"
)

// 回归：startRefresh 被 Reload 反复调用必须幂等。
// 泄漏一个「周期拉取+解析 XMLTV」goroutine 会在 update_interval 很短时
// 并发吃满 CPU（armv7 设备实测 360%）。
func TestEPGStartRefreshIdempotent(t *testing.T) {
	b := NewEPGBank()
	// 假地址 + 1h 间隔：测试期间 ticker 不会真的触发拉取
	b.startRefresh("http://127.0.0.1:1/x.xml", time.Hour)
	b.startRefresh("http://127.0.0.1:1/x.xml", time.Hour)
	b.startRefresh("http://127.0.0.1:1/x.xml", time.Hour)

	// 换 URL：旧循环必须退出（不残留 goroutine）
	before := runtime.NumGoroutine()
	b.startRefresh("http://127.0.0.1:1/y.xml", time.Hour)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before {
		t.Fatalf("旧刷新循环未退出: before=%d after=%d", before, n)
	}
}

// 回归：Load 在进行中/1 分钟内重复调用必须被节流，
// 避免 update_interval=1m 时每次 Reload 都全量下载+解析 XMLTV。
func TestEPGLoadThrottle(t *testing.T) {
	// httpclient.NewHTTPClient 依赖 config.Cfg.HTTP 的指针字段，测试里补默认
	no := false
	config.Cfg.HTTP.InsecureSkipVerify = &no
	config.Cfg.HTTP.DisableKeepAlives = &no

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><tv></tv>`))
	}))
	defer srv.Close()

	b := NewEPGBank()
	b.Load(srv.URL) // 首次：放行
	b.Load(srv.URL) // 进行中已结束但 1 分钟内：节流
	b.Load(srv.URL) // 同上

	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Fatalf("Load 未节流: 期望 1 次请求, 实际 %d", n)
	}
}
