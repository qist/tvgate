package proxy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/qist/tvgate/config"
)

// usageWindowSec 是节点在用带宽的统计窗口。
// HLS 分片通常 10s 一个、下载只占其中几秒，窗口太短会把「本轮还没下完」
// 平均成很低的速率；30s 能覆盖两三个分片周期，接近真实播放码率。
const usageWindowSec = 30

// usageMeter 按秒分桶累计节点实际下行字节，用于估算播放占用带宽。
type usageMeter struct {
	mu      sync.Mutex
	buckets [usageWindowSec]int64
	lastSec int64
	idx     int
}

func (m *usageMeter) add(n int64) {
	if n <= 0 {
		return
	}
	now := time.Now().Unix()
	m.mu.Lock()
	m.rotateLocked(now)
	m.buckets[m.idx] += n
	m.mu.Unlock()
}

func (m *usageMeter) rotateLocked(now int64) {
	if m.lastSec == 0 {
		m.lastSec = now
		return
	}
	delta := now - m.lastSec
	if delta <= 0 {
		return
	}
	if delta >= int64(len(m.buckets)) {
		m.buckets = [usageWindowSec]int64{}
		m.idx = 0
		m.lastSec = now
		return
	}
	for i := int64(0); i < delta; i++ {
		m.idx = (m.idx + 1) % len(m.buckets)
		m.buckets[m.idx] = 0
	}
	m.lastSec = now
}

// rate 返回最近一个窗口内的平均下行速率（字节/秒）。
func (m *usageMeter) rate() int64 {
	now := time.Now().Unix()
	m.mu.Lock()
	m.rotateLocked(now)
	var sum int64
	for _, v := range m.buckets {
		sum += v
	}
	m.mu.Unlock()
	return sum / int64(usageWindowSec)
}

var usageMeters sync.Map

func usageKey(proxyConfig config.ProxyConfig) string {
	return fmt.Sprintf("%s://%s:%d", strings.ToLower(proxyConfig.Type), proxyConfig.Server, proxyConfig.Port)
}

func meterFor(proxyConfig config.ProxyConfig) *usageMeter {
	key := usageKey(proxyConfig)
	if v, ok := usageMeters.Load(key); ok {
		return v.(*usageMeter)
	}
	m := &usageMeter{}
	v, _ := usageMeters.LoadOrStore(key, m)
	return v.(*usageMeter)
}

// UsageRate 返回该节点最近约 30s 的实际下行速率（字节/秒）。
// 只统计播放/代理请求读取的正文，不含探测流量（探测走 WithNoUsageCounting）。
func UsageRate(proxyConfig config.ProxyConfig) int64 {
	if v, ok := usageMeters.Load(usageKey(proxyConfig)); ok {
		return v.(*usageMeter).rate()
	}
	return 0
}

type noUsageKey struct{}

// WithNoUsageCounting 让本次请求的正文不计入节点在用带宽（用于探测）。
func WithNoUsageCounting(ctx context.Context) context.Context {
	return context.WithValue(ctx, noUsageKey{}, true)
}

func usageDisabled(ctx context.Context) bool {
	v, _ := ctx.Value(noUsageKey{}).(bool)
	return v
}

// usageTransport 在读取响应体时累计字节数；探测请求跳过。
type usageTransport struct {
	base  http.RoundTripper
	meter *usageMeter
}

func (t *usageTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil || usageDisabled(req.Context()) {
		return resp, err
	}
	resp.Body = &usageBody{ReadCloser: resp.Body, meter: t.meter}
	return resp, nil
}

type usageBody struct {
	io.ReadCloser
	meter *usageMeter
}

func (b *usageBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.meter.add(int64(n))
	}
	return n, err
}
