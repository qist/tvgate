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

// installXMLTV 测试辅助：把一份或多份 XMLTV 文本直接装进 EPGBank（跳过网络拉取），
// 等价于 Load 成功路径（parseEPGDataset + install），多份按参数顺序即优先级。
func installXMLTV(t *testing.T, b *EPGBank, bodies ...string) {
	t.Helper()
	sets := make([]*epgDataset, 0, len(bodies))
	for _, body := range bodies {
		ds, ok := parseEPGDataset([]byte(body))
		if !ok {
			t.Fatalf("测试 XMLTV 解析失败: %s", body)
		}
		sets = append(sets, ds)
	}
	b.install(sets)
}

// TestEPGLoadFollowsRedirect 回归：EPG 来源 301/302 跳到别的域名（如
// epg.51zmt.top:8000/e1.xml.gz → s.102031.xyz/xml/xxx.xml.gz）必须跟随，
// 否则整份节目单拉不到（基座 client 不自动跟随重定向）。
func TestEPGLoadFollowsRedirect(t *testing.T) {
	no := false
	config.Cfg.HTTP.InsecureSkipVerify = &no
	config.Cfg.HTTP.DisableKeepAlives = &no

	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<tv><channel id="1"><display-name lang="zh">CCTV1</display-name></channel>` +
			`<programme channel="1" start="20260901080000 +0800" stop="20260901090000 +0800"><title>朝闻天下</title></programme></tv>`))
	}))
	defer final.Close()
	redir := httptest.NewServer(http.RedirectHandler(final.URL, http.StatusMovedPermanently))
	defer redir.Close()

	b := NewEPGBank()
	b.Load(redir.URL)
	if !b.HaveData() {
		t.Fatal("重定向未跟随：整份 EPG 未加载")
	}
	if ps := b.Programs("CCTV1", "20260901"); len(ps) != 1 || ps[0].Title != "朝闻天下" {
		t.Fatalf("重定向后查询不对: %+v", ps)
	}
}

// TestEPGSourcesParse 配置展开：epg 支持换行/分号；逗号只在"每段都像来源"时才拆
// （URL 查询串里的逗号要保住）；epgs 列表追加并按原序去重。
func TestEPGSourcesParse(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.PlayerConfig
		want []string
	}{
		{"单源", config.PlayerConfig{Epg: "https://a.example.com/e.xml.gz"},
			[]string{"https://a.example.com/e.xml.gz"}},
		{"换行分隔", config.PlayerConfig{Epg: "https://a/e.xml.gz\nhttps://b/e.xml.gz"},
			[]string{"https://a/e.xml.gz", "https://b/e.xml.gz"}},
		{"分号分隔", config.PlayerConfig{Epg: "https://a/e.xml.gz; https://b/?ch={name}&date={date}"},
			[]string{"https://a/e.xml.gz", "https://b/?ch={name}&date={date}"}},
		{"逗号分隔(每段都像来源)", config.PlayerConfig{Epg: "https://a/e.xml.gz,https://b/e.xml.gz"},
			[]string{"https://a/e.xml.gz", "https://b/e.xml.gz"}},
		{"查询串里的逗号不拆", config.PlayerConfig{Epg: "https://a/epg?ids=1,2&date=20260918"},
			[]string{"https://a/epg?ids=1,2&date=20260918"}},
		{"epgs 追加并去重", config.PlayerConfig{
			Epg:  "https://a/?ch={name}&date={date}",
			Epgs: []string{"https://b/e.xml.gz", " ", "https://a/?ch={name}&date={date}"},
		}, []string{"https://a/?ch={name}&date={date}", "https://b/e.xml.gz"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := epgSources(c.cfg)
			if len(got) != len(c.want) {
				t.Fatalf("来源数不对: got %v want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("第 %d 个来源不对: got %v want %v", i, got, c.want)
				}
			}
		})
	}

	// 类型判定：含 { → template；http 开头 → xml；其它忽略
	for _, c := range []struct{ in, wantType string }{
		{"https://a/e.xml.gz", "xml"},
		{"http://epg.51zmt.top:8000/e1.xml.gz", "xml"},
		{"https://a/?ch={name}&date={date}", "template"},
		{"", "none"},
		{"/opt/tvgate/e.xml.gz", "none"},
	} {
		if got := classifyEPGSource(c.in); got.Type != c.wantType {
			t.Fatalf("classifyEPGSource(%q)=%q，期望 %q", c.in, got.Type, c.wantType)
		}
	}
}

// 回归：全局配置处在"默认值未补齐"的窗口（config.Cfg.HTTP 的 *bool 为 nil）时，
// 重载触发的 EPG 整份 XMLTV 拉取不能空指针 panic（实测事故：后台保存配置 → config/load
// 发布 nil 配置 → 出口通知播放器重载 → 本路径 NewHTTPClient 解引用 nil，进程被带走）。
func TestEPGLoadWithNilHTTPConfig(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<tv><channel id="1"><display-name lang="zh">CCTV1</display-name></channel>` +
			`<programme channel="1" start="20260901080000 +0800" stop="20260901090000 +0800"><title>朝闻天下</title></programme></tv>`))
	}))
	defer up.Close()

	// 模拟刚 Unmarshal 出来、尚未 SetDefaults 的配置（这两个 *bool 为 nil）
	oldInsecure, oldKeepAlive := config.Cfg.HTTP.InsecureSkipVerify, config.Cfg.HTTP.DisableKeepAlives
	config.Cfg.HTTP.InsecureSkipVerify, config.Cfg.HTTP.DisableKeepAlives = nil, nil
	t.Cleanup(func() {
		config.Cfg.HTTP.InsecureSkipVerify, config.Cfg.HTTP.DisableKeepAlives = oldInsecure, oldKeepAlive
	})

	b := NewEPGBank()
	b.Load(up.URL)
	if ps := b.Programs("CCTV1", "20260901"); len(ps) != 1 || ps[0].Title != "朝闻天下" {
		t.Fatalf("nil 指针窗口期内 EPG 拉取异常: %+v", ps)
	}
}

// 回归：startRefresh 被 Reload 反复调用必须幂等。
// 泄漏一个「周期拉取+解析 XMLTV」goroutine 会在 update_interval 很短时
// 并发吃满 CPU（armv7 设备实测 360%）。
func TestEPGStartRefreshIdempotent(t *testing.T) {
	b := NewEPGBank()
	// 假地址 + 1h 间隔：测试期间 ticker 不会真的触发拉取
	b.startRefresh(time.Hour, "http://127.0.0.1:1/x.xml")
	b.startRefresh(time.Hour, "http://127.0.0.1:1/x.xml")
	b.startRefresh(time.Hour, "http://127.0.0.1:1/x.xml")

	// 换 URL：旧循环必须退出（不残留 goroutine）
	before := runtime.NumGoroutine()
	b.startRefresh(time.Hour, "http://127.0.0.1:1/y.xml")

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

// TestEPGMergeMultipleSources 多份 XMLTV 查询时合并（来源 A 优先）：
//   - 同一频道（display-name 相同、channel id 不同）→ 节目并集、按时间排序；
//   - 同 start 取靠前来源（不出现重复时段）；
//   - 只有某个来源有的频道照样能查到；
//   - 各来源内部的精确名/归一化名规则独立生效。
func TestEPGMergeMultipleSources(t *testing.T) {
	// 来源 A（优先级高）：CCTV1 08:00、CCTV2 09:00；display-name 归一化到 "cctv1"
	a := `<tv>` +
		`<channel id="a1"><display-name lang="zh">CCTV1</display-name></channel>` +
		`<channel id="a2"><display-name lang="zh">CCTV-2</display-name></channel>` +
		`<programme channel="a1" start="20260901080000 +0800" stop="20260901090000 +0800"><title>A·朝闻天下</title></programme>` +
		`<programme channel="a2" start="20260901090000 +0800" stop="20260901100000 +0800"><title>A·经济半小时</title></programme>` +
		`</tv>`
	// 来源 B：CCTV1 同 08:00（应被 A 压制）+ 12:00；另有 A 没有的 CCTV3；"CCTV2" 与 A 的 "CCTV-2" 归一化冲突
	b := `<tv>` +
		`<channel id="b1"><display-name lang="zh">CCTV1</display-name></channel>` +
		`<channel id="b2"><display-name lang="zh">CCTV2</display-name></channel>` +
		`<channel id="b3"><display-name lang="zh">CCTV3</display-name></channel>` +
		`<programme channel="b1" start="20260901080000 +0800" stop="20260901090000 +0800"><title>B·同档节目</title></programme>` +
		`<programme channel="b1" start="20260901120000 +0800" stop="20260901130000 +0800"><title>B·午间新闻</title></programme>` +
		`<programme channel="b3" start="20260901100000 +0800" stop="20260901110000 +0800"><title>B·综艺</title></programme>` +
		`</tv>`

	bank := NewEPGBank()
	installXMLTV(t, bank, a, b)

	// CCTV1：A(id a1) 的 08:00 + B(id b1) 的 12:00；B 的 08:00 与 A 同档被压制。
	// 两个来源 channel id 不同，靠 display-name 对上 → 这正是多接口合并的关键场景。
	ps := bank.Programs("CCTV1", "20260901")
	if len(ps) != 2 {
		t.Fatalf("CCTV1 应合并出 2 条: %+v", ps)
	}
	if ps[0].Title != "A·朝闻天下" || ps[1].Title != "B·午间新闻" {
		t.Fatalf("CCTV1 合并/排序/去重不对: %+v", ps)
	}
	// 按 channel id 精确查只命中该来源自己的节目（各来源 id 独立）
	if ps := bank.Programs("a1", "20260901"); len(ps) != 1 || ps[0].Title != "A·朝闻天下" {
		t.Fatalf("按 channel id 查询不对: %+v", ps)
	}
	// 只在 B 里有的频道仍可查（并集）
	if ps := bank.Programs("CCTV3", "20260901"); len(ps) != 1 || ps[0].Title != "B·综艺" {
		t.Fatalf("并集丢失 B 独有频道: %+v", ps)
	}
	// 归一化模糊匹配在各来源内独立生效：A 的 "CCTV-2" 归一到 cctv2 可被 "cctv2" 命中
	if ps := bank.Programs("cctv2", "20260901"); len(ps) != 1 || ps[0].Title != "A·经济半小时" {
		t.Fatalf("归一化模糊匹配不对: %+v", ps)
	}
	// 精确名优先命中自己的频道
	if ps := bank.Programs("CCTV-2", "20260901"); len(ps) != 1 || ps[0].Title != "A·经济半小时" {
		t.Fatalf("精确名查询不对: %+v", ps)
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
