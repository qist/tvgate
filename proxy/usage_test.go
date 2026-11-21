package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qist/tvgate/config"
)

func newUsageTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, 64<<10))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestUsageRateCountsBodyBytes(t *testing.T) {
	srv := newUsageTestServer(t)
	pc := config.ProxyConfig{Type: "http", Server: "127.0.0.1", Port: 18081}
	client := &http.Client{Transport: &usageTransport{base: http.DefaultTransport, meter: meterFor(pc)}}

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if got := UsageRate(pc); got <= 0 {
		t.Fatalf("应统计到在用带宽，got %d", got)
	}
}

func TestUsageRateSkipsProbeRequests(t *testing.T) {
	srv := newUsageTestServer(t)
	pc := config.ProxyConfig{Type: "http", Server: "127.0.0.1", Port: 18082}
	client := &http.Client{Transport: &usageTransport{base: http.DefaultTransport, meter: meterFor(pc)}}

	ctx := WithNoUsageCounting(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if got := UsageRate(pc); got != 0 {
		t.Fatalf("探测流量不应计入在用带宽，got %d", got)
	}
}
