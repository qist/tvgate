package player

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/php"
)

// setTestPlayer 写入全局播放器配置供 Reload 读取，测试结束还原。
func setTestPlayer(p config.PlayerConfig, t *testing.T) {
	old := config.Cfg.Player
	config.Cfg.Player = p
	t.Cleanup(func() { config.Cfg.Player = old })
}

func TestManagerReloadAndChannels(t *testing.T) {
	// httpclient.NewHTTPClient 依赖 config.Cfg.HTTP 的指针字段，测试里补默认
	b := false
	config.Cfg.HTTP.InsecureSkipVerify = &b
	config.Cfg.HTTP.DisableKeepAlives = &b

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("央视,#genre#\nCCTV1,rtsp://10.0.0.1:554/live/c1.smil\nCCTV2,http://10.0.0.2/live/2.m3u8\n"))
	}))
	defer up.Close()

	setTestPlayer(config.PlayerConfig{Enabled: true, Subscription: up.URL}, t)

	mgr := NewManager(&config.Cfg.Player)
	mgr.httpClient = up.Client()
	mgr.Reload()

	chans := mgr.Channels()
	if len(chans) != 2 {
		t.Fatalf("期望 2 频道, got %d", len(chans))
	}
	var rtspCh *Channel
	for _, c := range chans {
		if c.Name == "CCTV1" {
			rtspCh = c
		}
	}
	// key 稳定且不暴露 RawURL
	if rtspCh == nil || rtspCh.Key == "" || rtspCh.RawURL != "rtsp://10.0.0.1:554/live/c1.smil" {
		t.Fatalf("key/rawurl 不对: %+v", rtspCh)
	}

	h := NewHandler(mgr)
	h.httpClient = up.Client()

	// /api/player/channels 序列化时不得带 RawURL
	rr := httptest.NewRecorder()
	h.ServeChannels(rr, httptest.NewRequest("GET", "/api/player/channels", nil))
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("channels json 解析失败: %v", err)
	}
	raw := rr.Body.String()
	if strings.Contains(raw, "10.0.0.1") || strings.Contains(raw, "RawURL") {
		t.Fatalf("channels 响应泄漏了源地址: %s", raw)
	}

	// 未知 key → 403
	rr2 := httptest.NewRecorder()
	h.ServePull(rr2, httptest.NewRequest("GET", "/player/nonexistent", nil))
	if rr2.Code != http.StatusForbidden {
		t.Fatalf("未知 key 应 403, got %d", rr2.Code)
	}
}

func TestTxtEpgTemplateConfig(t *testing.T) {
	b := false
	config.Cfg.HTTP.InsecureSkipVerify = &b
	config.Cfg.HTTP.DisableKeepAlives = &b
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("央视,#genre#\nCCTV1,rtsp://10.0.0.1/live/c1.smil\n"))
	}))
	defer up.Close()

	// 订阅内容内未带 epg= 行 → 用配置 player.epg 模板
	setTestPlayer(config.PlayerConfig{
		Enabled:      true,
		Subscription: up.URL,
		Epg:          "https://<your-domain>/?ch={name}&date={date}",
	}, t)
	mgr := NewManager(&config.Cfg.Player)
	mgr.httpClient = up.Client()
	mgr.Reload()

	es := mgr.EPGSource()
	if es.Type != "template" || es.URL != "https://<your-domain>/?ch={name}&date={date}" {
		t.Fatalf("配置 epg 模板未生效: %+v", es)
	}
}

// TestM3UEmbeddedEpgConfigFallback M3U 内嵌 x-tvg-url（xml）存在时，配置 player.epg
// 的固定 XMLTV 自动并入 xml 来源集（epgURLs = [内嵌, 配置]），两份数据由 EPGBank 合并；
// 内嵌为 template 时进 epgTpls，配置固定 XMLTV 进 epgURLs（互为补齐）。
func TestM3UEmbeddedEpgConfigFallback(t *testing.T) {
	b := false
	config.Cfg.HTTP.InsecureSkipVerify = &b
	config.Cfg.HTTP.DisableKeepAlives = &b

	// M3U 内嵌 x-tvg-url（整份 XMLTV 地址）
	inner := "https://epg-inner.example.com/epg.xml.gz"
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("#EXTM3U x-tvg-url=\"" + inner + "\"\n#EXTINF:-1 tvg-id=\"c1\",CCTV1\nhttp://10.0.0.1/c1.m3u8\n"))
	}))
	defer up.Close()

	// 配置：固定 XMLTV（外部 xml.gz 兜底）
	setTestPlayer(config.PlayerConfig{
		Enabled:      true,
		Subscription: up.URL,
		Epg:          "https://example.com/e.xml.gz",
	}, t)
	mgr := NewManager(&config.Cfg.Player)
	mgr.httpClient = up.Client()
	mgr.Reload()

	es := mgr.EPGSource()
	if es.Type != "xml" || es.URL != inner {
		t.Fatalf("M3U 内嵌 x-tvg-url 应为主来源: %+v", es)
	}
	mgr.mu.RLock()
	urls := append([]string(nil), mgr.epgURLs...)
	tpls := append([]string(nil), mgr.epgTpls...)
	mgr.mu.RUnlock()
	if len(urls) != 2 || urls[0] != inner || urls[1] != "https://example.com/e.xml.gz" {
		t.Fatalf("xml 来源集组装不对: %v", urls)
	}
	if len(tpls) != 0 {
		t.Fatalf("同型来源不应进模板集: %v", tpls)
	}

	// 内嵌为 template、配置固定 XMLTV → 模板进 epgTpls，XMLTV 进 epgURLs（互补）
	up2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("央视,#genre#\nepg=https://tpl.example.com/?ch={name}&date={date}\nCCTV1,rtsp://10.0.0.2/c1\n"))
	}))
	defer up2.Close()
	tpl := config.PlayerConfig{
		Enabled:      true,
		Subscription: up2.URL,
		Epg:          "https://example.com/e.xml.gz",
	}
	setTestPlayer(tpl, t)
	mgr2 := NewManager(&config.Cfg.Player)
	mgr2.httpClient = up2.Client()
	mgr2.Reload()
	mgr2.mu.RLock()
	tpls2 := append([]string(nil), mgr2.epgTpls...)
	urls2 := append([]string(nil), mgr2.epgURLs...)
	mgr2.mu.RUnlock()
	if len(tpls2) != 1 || tpls2[0] != "https://tpl.example.com/?ch={name}&date={date}" {
		t.Fatalf("内嵌模板应进 epgTpls: %v", tpls2)
	}
	if len(urls2) != 1 || urls2[0] != "https://example.com/e.xml.gz" {
		t.Fatalf("配置固定 XMLTV 应进 epgURLs: %v", urls2)
	}
}

// TestSubscriptionFollowsRedirect 订阅源 301/302 跟随：
// http → https 升级、域名/CDN 搬迁很常见（源站把 http 跳 https），不跟随就等于订阅拉不到。
// 这里让 http 源 301 到一个 **https** 源，验证跨协议跳转后仍能正常解析出频道表。
func TestSubscriptionFollowsRedirect(t *testing.T) {
	no := false
	config.Cfg.HTTP.InsecureSkipVerify = &no
	config.Cfg.HTTP.DisableKeepAlives = &no

	// 最终订阅（https，自签证书由 httptest 的 client 信任）
	final := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("央视,#genre#\nCCTV1,http://192.0.2.1/live/1.m3u8\n"))
	}))
	defer final.Close()
	// 入口（http）301 到 https 订阅
	entry := httptest.NewServer(http.RedirectHandler(final.URL, http.StatusMovedPermanently))
	defer entry.Close()

	setTestPlayer(config.PlayerConfig{Enabled: true, Subscription: entry.URL}, t)
	mgr := NewManager(&config.Cfg.Player)
	mgr.httpClient = final.Client()
	mgr.Reload()

	chans := mgr.Channels()
	if len(chans) != 1 || chans[0].Name != "CCTV1" {
		t.Fatalf("重定向后的订阅未解析: %+v", chans)
	}

	// 重定向到不支持协议（file://）必须拒绝，不能当成本地文件去读
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "file:///etc/passwd")
		w.WriteHeader(http.StatusFound)
	}))
	defer bad.Close()
	setTestPlayer(config.PlayerConfig{Enabled: true, Subscription: bad.URL}, t)
	mgr2 := NewManager(&config.Cfg.Player)
	mgr2.httpClient = bad.Client()
	mgr2.Reload()
	if len(mgr2.Channels()) != 0 {
		t.Fatalf("不应把 file:// 重定向当订阅解析: %+v", mgr2.Channels())
	}
}

func TestFetchSubscriptionSources(t *testing.T) {
	b := false
	config.Cfg.HTTP.InsecureSkipVerify = &b
	config.Cfg.HTTP.DisableKeepAlives = &b

	tmp := t.TempDir()
	content := "央视,#genre#\nCCTV1,rtsp://10.0.0.1/live/c1.smil\n"
	if err := os.WriteFile(filepath.Join(tmp, "channels.txt"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	oldDoc := config.Cfg.PHP.DocRoot
	config.Cfg.PHP.DocRoot = tmp
	t.Cleanup(func() { config.Cfg.PHP.DocRoot = oldDoc })

	m := NewManager(&config.PlayerConfig{Enabled: true, Subscription: ""})
	m.httpClient = http.DefaultClient

	cases := map[string]string{
		"绝对路径":    filepath.Join(tmp, "channels.txt"),
		"file://": "file://" + filepath.Join(tmp, "channels.txt"),
		"php://":  "php://channels.txt", // 相对 docroot
		"相对路径":    "channels.txt",       // 相对 docroot
	}
	for name, src := range cases {
		if got := string(m.fetch(src)); got != content {
			t.Fatalf("[%s] 读取不符: got=%q", name, got)
		}
	}

	// HTTP(S) 源
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(content))
	}))
	defer up.Close()
	m.httpClient = up.Client()
	if got := string(m.fetch(up.URL)); got != content {
		t.Fatalf("[http] 读取不符: got=%q", got)
	}
}

// TestFetchAllDir 目录订阅：递归收集 .txt/.m3u，跳过隐藏/无关文件，按名排序合并；单文件与 http 委托原逻辑。
func TestFetchAllDir(t *testing.T) {
	b := false
	config.Cfg.HTTP.InsecureSkipVerify = &b
	config.Cfg.HTTP.DisableKeepAlives = &b

	tmp := t.TempDir()
	sub := filepath.Join(tmp, "tv")
	if err := os.MkdirAll(filepath.Join(sub, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	write := func(p, c string) {
		if err := os.WriteFile(p, []byte(c), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(sub, "b_mig.txt"), "咪咕,#genre#\nCCTV1,rtsp://10.0.0.1/c1\n")
	write(filepath.Join(sub, "a_bestv.txt"), "百视通,#genre#\nCCTV2,http://10.0.0.2/c2\n")
	write(filepath.Join(sub, "sub", "c_iptv.m3u"), "#EXTM3U\n#EXTINF:-1,CCTV3\nhttp://10.0.0.3/c3\n")
	write(filepath.Join(sub, "ignored.md"), "不是订阅")
	write(filepath.Join(sub, ".hidden.txt"), "咪咕,#genre#\nX,http://10.0.0.9/x\n")
	write(filepath.Join(sub, "empty.txt"), "")

	m := NewManager(&config.PlayerConfig{Enabled: true})
	files := m.fetchAll(sub)
	if len(files) != 3 {
		t.Fatalf("期望 3 个订阅文件（跳过隐藏/空/无关），got %d: %+v", len(files), files)
	}
	// 排序：a_bestv.txt < b_mig.txt < sub/c_iptv.m3u
	if !strings.HasSuffix(files[0].name, "a_bestv.txt") || !strings.HasSuffix(files[1].name, "b_mig.txt") || !strings.HasSuffix(files[2].name, "c_iptv.m3u") {
		t.Fatalf("排序不符: %v", []string{files[0].name, files[1].name, files[2].name})
	}

	// Reload 合并：3 频道、3 分组、按文件序
	m.cfg = &config.PlayerConfig{Enabled: true, Subscription: sub}
	config.Cfg.Player = *m.cfg
	t.Cleanup(func() { config.Cfg.Player = config.PlayerConfig{} })
	m.Reload()
	cs := m.Channels()
	if len(cs) != 3 {
		t.Fatalf("期望合并 3 频道, got %d", len(cs))
	}
	if cs[0].Name != "CCTV2" || cs[1].Name != "CCTV1" || cs[2].Name != "CCTV3" {
		t.Fatalf("合并顺序不符: %s, %s, %s", cs[0].Name, cs[1].Name, cs[2].Name)
	}
	if gs := m.Groups(); len(gs) != 3 {
		t.Fatalf("期望 3 分组, got %v", gs)
	}
}

// TestSubscriptionSourcesSplit 多订阅源展开：分隔符、去重、以及"路径里带逗号"不被误切。
func TestSubscriptionSourcesSplit(t *testing.T) {
	got := subscriptionSources(config.PlayerConfig{
		Subscription:  " http://a/x.txt \nhttp://b/y.txt,http://c/z.txt;http://a/x.txt ",
		Subscriptions: []string{"http://d/w.txt, http://e/v.txt", "", "http://b/y.txt"},
	})
	want := []string{"http://a/x.txt", "http://b/y.txt", "http://c/z.txt", "http://d/w.txt", "http://e/v.txt"}
	if len(got) != len(want) {
		t.Fatalf("源数不符: got=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 个源不符: got=%q want=%q（应为原序去重）", i, got[i], want[i])
		}
	}

	// 本地路径里含逗号：整串本身存在 → 按单源处理，不切开
	tmp := t.TempDir()
	weird := filepath.Join(tmp, "tv,backup.txt")
	if err := os.WriteFile(weird, []byte("甲,#genre#\nA,rtsp://10.0.0.1/a.smil\n"), 0644); err != nil {
		t.Fatal(err)
	}
	one := subscriptionSources(config.PlayerConfig{Subscription: weird})
	if len(one) != 1 || one[0] != weird {
		t.Fatalf("带逗号的本地路径应作为单源: %v", one)
	}
}

// TestMultiSubscriptionSources 多订阅合并：subscription 内多源 + subscriptions 列表 + 失效源跳过。
func TestMultiSubscriptionSources(t *testing.T) {
	b := false
	config.Cfg.HTTP.InsecureSkipVerify = &b
	config.Cfg.HTTP.DisableKeepAlives = &b

	subA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("央视,#genre#\nCCTV1,rtsp://10.0.0.1/c1.smil\n"))
	}))
	defer subA.Close()
	subB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("新江苏移动,#genre#\n凤凰资讯,rtsp://10.0.0.2/fh.smil\nCCTV1,rtsp://10.0.0.9/c1-hd.smil\n"))
	}))
	defer subB.Close()
	subC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("补充,#genre#\nCCTV2,rtsp://10.0.0.3/c2.smil\n"))
	}))
	defer subC.Close()

	setTestPlayer(config.PlayerConfig{
		Enabled: true,
		// subscription 一栏里写两个源（换行分隔）
		Subscription: subA.URL + "\n" + subB.URL,
		// 列表再追加两个源，其中一个是必然失效的地址、一个是 subA 的重复源
		Subscriptions: []string{subC.URL, "http://127.0.0.1:1/dead.txt", subA.URL},
	}, t)
	mgr := NewManager(&config.Cfg.Player)
	mgr.httpClient = subA.Client()
	mgr.Reload()

	chans := mgr.Channels()
	want := []string{"CCTV1", "凤凰资讯", "CCTV1", "CCTV2"} // 两条 CCTV1 的 URL 不同 → 均保留
	if len(chans) != len(want) {
		t.Fatalf("频道数不符: got %d want %d (%+v)", len(chans), len(want), chans)
	}
	for i := range want {
		if chans[i].Name != want[i] {
			t.Fatalf("第 %d 个频道应为 %s，got %s（应按源顺序合并）", i, want[i], chans[i].Name)
		}
	}
	groups := mgr.Groups()
	if len(groups) != 3 {
		t.Fatalf("期望 3 分组, got %v", groups)
	}
	// 失效源被跳过，其余源照常加载；重复源不重复解析
	if got := len(subscriptionSources(config.Cfg.Player)); got != 4 {
		t.Fatalf("展开后的源数应为 4（去重后），got %d", got)
	}
	if !mgr.Enabled() {
		t.Fatal("有可用订阅源时 Enabled 应为 true")
	}
}

// TestMultiSubscriptionAllSourcesFail 全部源失效时保留上一次频道表（不清空）。
func TestMultiSubscriptionAllSourcesFail(t *testing.T) {
	b := false
	config.Cfg.HTTP.InsecureSkipVerify = &b
	config.Cfg.HTTP.DisableKeepAlives = &b

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("甲,#genre#\nA1,rtsp://10.0.0.1/a.smil\n"))
	}))
	defer up.Close()

	setTestPlayer(config.PlayerConfig{Enabled: true, Subscription: up.URL}, t)
	mgr := NewManager(&config.Cfg.Player)
	mgr.httpClient = up.Client()
	mgr.Reload()
	if len(mgr.Channels()) != 1 {
		t.Fatalf("前置条件不成立: %+v", mgr.Channels())
	}

	// 换成两个都失效的源：本次刷新放弃，但不得清空已有频道表
	setTestPlayer(config.PlayerConfig{
		Enabled:       true,
		Subscription:  "http://127.0.0.1:1/a.txt",
		Subscriptions: []string{"http://127.0.0.1:1/b.txt"},
	}, t)
	mgr.httpClient = up.Client()
	mgr.Reload()
	if len(mgr.Channels()) != 1 {
		t.Fatalf("全部源失效时不应清空频道表: %+v", mgr.Channels())
	}
}

// TestFetchPHPScriptSource php:// 脚本源：订阅源写成 php://xxx.php?query 时由内嵌 phpgo 执行，
// 输出即订阅内容；带/不带 `php/` 前缀两种写法都要能用（与频道源 php:// 同一约定）。
func TestFetchPHPScriptSource(t *testing.T) {
	// php.Init 会构造 PHP 专用 HTTP client（需要 HTTP 配置的指针字段，与其它测试一致地补默认）
	b := false
	config.Cfg.HTTP.InsecureSkipVerify = &b
	config.Cfg.HTTP.DisableKeepAlives = &b

	tmp := t.TempDir()
	oldRoot := config.Cfg.PHP.DocRoot
	config.Cfg.PHP.DocRoot = tmp
	t.Cleanup(func() { config.Cfg.PHP.DocRoot = oldRoot })
	php.Init(&config.Cfg)

	script := "<?php\necho \"甲,#genre#\\n\";\necho \"A\" . (isset($_GET['id']) ? $_GET['id'] : 'none') . \",rtsp://10.0.0.1/a.smil\\n\";\n"
	if err := os.WriteFile(filepath.Join(tmp, "sub.php"), []byte(script), 0644); err != nil {
		t.Fatal(err)
	}

	for _, src := range []string{"php://sub.php?id=all", "php://php/sub.php?id=all"} {
		setTestPlayer(config.PlayerConfig{Enabled: true, Subscription: src}, t)
		mgr := NewManager(&config.Cfg.Player)
		mgr.Reload()
		chans := mgr.Channels()
		if len(chans) != 1 || chans[0].Name != "Aall" {
			t.Fatalf("[%s] php 脚本源未生效（应解析出 Aall）: %+v", src, chans)
		}
	}

	// 单源字段里的 php 脚本源不应被分隔符切开（query 里可能带逗号）
	got := subscriptionSources(config.PlayerConfig{Subscription: "php://sub.php?id=all,hd"})
	if len(got) != 1 {
		t.Fatalf("php 脚本源被误切: %v", got)
	}

	// 脚本不存在的 php 源：拉取失败但不 panic（Reload 读全局配置，须同步设置）
	setTestPlayer(config.PlayerConfig{Enabled: true, Subscription: "php://missing.php"}, t)
	m := NewManager(&config.Cfg.Player)
	m.Reload()
	if len(m.Channels()) != 0 {
		t.Fatalf("不存在的 php 源不应产出频道: %+v", m.Channels())
	}
}

// TestHotReloadNotifyReloadsSubscription 配置热加载通知链路：player 段变化后 NotifyConfigChanged
// 必须让管理器立即按新源重载（多订阅加源即时生效就靠它）。
func TestHotReloadNotifyReloadsSubscription(t *testing.T) {
	b := false
	config.Cfg.HTTP.InsecureSkipVerify = &b
	config.Cfg.HTTP.DisableKeepAlives = &b

	subA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("甲,#genre#\nA1,rtsp://10.0.0.1/a.smil\n"))
	}))
	defer subA.Close()
	subB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("乙,#genre#\nB1,rtsp://10.0.0.2/b.smil\n"))
	}))
	defer subB.Close()

	setTestPlayer(config.PlayerConfig{Enabled: true, Subscription: subA.URL}, t)
	mgr := NewManager(&config.Cfg.Player)
	mgr.httpClient = subA.Client()
	prevHandler := globalHandler
	globalHandler = NewHandler(mgr)
	t.Cleanup(func() {
		globalHandler = prevHandler
		mgr.Stop()
	})
	mgr.Start()

	waitChans := func(want int, what string) {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if len(mgr.Channels()) == want {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("%s：期望 %d 个频道，got %d (%+v)", what, want, len(mgr.Channels()), mgr.Channels())
	}
	waitChans(1, "首次加载")

	// 模拟后台改配置：追加 subscriptions 源 → 通知 → 立即重载
	p := config.Cfg.Player
	p.Subscriptions = []string{subB.URL}
	config.Cfg.Player = p
	NotifyConfigChanged()
	waitChans(2, "热加载追加订阅源后")
}

// 回归：配置已被别的路径（后台配置页保存 / publisher 监听）先读进内存时，通知不能因为
// "加载前后 config.Cfg.Player 一样"而被判定为无变化丢掉 —— 实测现象是给配置加上
// player.logo 后前台台标永远不出现、必须重启。NotifyPlayerConfigChanged 与"已应用的配置"
// 对比，谁先谁后都能触发重载。
func TestNotifyPlayerConfigChangedAfterExternalLoad(t *testing.T) {
	b := false
	config.Cfg.HTTP.InsecureSkipVerify = &b
	config.Cfg.HTTP.DisableKeepAlives = &b

	// 上游订阅被拉取的次数：用来验证"没变化的重复通知不会白拉一遍订阅"
	var subFetches atomic.Int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		subFetches.Add(1)
		w.Write([]byte("甲,#genre#\nA1,rtsp://10.0.0.1/a.smil\n"))
	}))
	defer up.Close()

	setTestPlayer(config.PlayerConfig{Enabled: true, Subscription: up.URL}, t)
	mgr := NewManager(&config.Cfg.Player)
	mgr.httpClient = up.Client()
	prevHandler := globalHandler
	globalHandler = NewHandler(mgr)
	t.Cleanup(func() {
		globalHandler = prevHandler
		mgr.Stop()
	})
	mgr.Start()

	waitLogo := func(want string, what string) {
		deadline := time.Now().Add(3 * time.Second)
		var got string
		for time.Now().Before(deadline) {
			chans := mgr.Channels()
			if len(chans) == 1 {
				got = chans[0].TVGLogo
				if got == want {
					return
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("%s：期望台标 %q，got %q", what, want, got)
	}
	waitLogo("", "首次加载（无 logo 模板）")

	// 同一份配置重复通知（加载出口每次 load 都会调）：与已应用的相同 → 空转，不再拉一次订阅
	fetchesBefore := subFetches.Load()
	NotifyPlayerConfigChanged(config.Cfg.Player)
	time.Sleep(200 * time.Millisecond)
	if got := subFetches.Load(); got != fetchesBefore {
		t.Fatalf("配置没变时不应重载订阅：拉取次数 %d → %d", fetchesBefore, got)
	}

	// 模拟后台保存配置：新配置（含 logo 模板）已被别的路径 load 进内存，但没通知过播放器
	next := config.Cfg.Player
	next.Logo = "https://logo.example.com/{name}.png"
	config.Cfg.Player = next

	// 加载出口的兜底通知：必须让播放器按新配置重载订阅（台标由此补上）
	NotifyPlayerConfigChanged(next)
	waitLogo("https://logo.example.com/A1.png", "配置加载通知后")
	if got := subFetches.Load(); got <= fetchesBefore {
		t.Fatalf("配置变化后应重载订阅：拉取次数仍为 %d", got)
	}
}

func TestReloadPicksUpConfigChange(t *testing.T) {
	b := false
	config.Cfg.HTTP.InsecureSkipVerify = &b
	config.Cfg.HTTP.DisableKeepAlives = &b

	subA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("甲,#genre#\nA1,rtsp://10.0.0.1/a.smil\n"))
	}))
	defer subA.Close()
	subB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("乙,#genre#\nB1,rtsp://10.0.0.2/b.smil\n"))
	}))
	defer subB.Close()

	setTestPlayer(config.PlayerConfig{Enabled: true, Subscription: subA.URL}, t)
	mgr := NewManager(&config.Cfg.Player)
	mgr.httpClient = subA.Client()
	mgr.Reload()
	if len(mgr.Channels()) != 1 || mgr.Channels()[0].Name != "A1" {
		t.Fatalf("初始订阅 A 未生效: %+v", mgr.Channels())
	}

	// 模拟热重载：只改全局配置的 subscription（切到 B 源），再 Reload
	config.Cfg.Player.Subscription = subB.URL
	mgr.httpClient = subB.Client()
	mgr.Reload()
	chans := mgr.Channels()
	if len(chans) != 1 || chans[0].Name != "B1" {
		t.Fatalf("配置变更后未生效（应切到 B1）: %+v", chans)
	}
}

func TestChannelOrderPreserved(t *testing.T) {
	b := false
	config.Cfg.HTTP.InsecureSkipVerify = &b
	config.Cfg.HTTP.DisableKeepAlives = &b

	// 订阅按 txt 顺序给出分组与频道
	body := "爱看咪咕,#genre#\nA1,rtsp://10.0.0.1/a.smil\nA2,rtsp://10.0.0.1/a2.smil\nA3,rtsp://10.0.0.1/a3.smil\n谷豆,#genre#\nB1,rtsp://10.0.0.2/b.smil\nB2,rtsp://10.0.0.2/b2.smil\n"
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
	defer up.Close()

	setTestPlayer(config.PlayerConfig{Enabled: true, Subscription: up.URL}, t)
	mgr := NewManager(&config.Cfg.Player)
	mgr.httpClient = up.Client()
	mgr.Reload()

	chans := mgr.Channels()
	want := []string{"A1", "A2", "A3", "B1", "B2"}
	if len(chans) != len(want) {
		t.Fatalf("频道数不符: got %d want %d", len(chans), len(want))
	}
	for i, w := range want {
		if chans[i].Name != w {
			t.Fatalf("顺序不符 idx=%d got=%s want=%s", i, chans[i].Name, w)
		}
	}
	// 分组顺序也按 txt
	if gs := mgr.Groups(); len(gs) != 2 || gs[0] != "爱看咪咕" || gs[1] != "谷豆" {
		t.Fatalf("分组顺序不符: %v", gs)
	}
}

func TestLocalLogoDir(t *testing.T) {
	b := false
	config.Cfg.HTTP.InsecureSkipVerify = &b
	config.Cfg.HTTP.DisableKeepAlives = &b

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("央视,#genre#\nCCTV1,rtsp://10.0.0.1/live/c1.smil\n"))
	}))
	defer up.Close()

	logoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(logoDir, "CCTV1.png"), []byte("PNGDATA"), 0644); err != nil {
		t.Fatal(err)
	}

	setTestPlayer(config.PlayerConfig{Enabled: true, Subscription: up.URL, LogoDir: logoDir}, t)
	mgr := NewManager(&config.Cfg.Player)
	mgr.httpClient = up.Client()
	mgr.Reload()
	if len(mgr.Channels()) != 1 {
		t.Fatalf("频道数不符")
	}
	c := mgr.Channels()[0]
	if c.TVGLogo != "/player/logo/CCTV1.png" {
		t.Fatalf("本地台标未生效: %q", c.TVGLogo)
	}
}

func TestParseEPGContent(t *testing.T) {
	// epg.cdn.loc.cc 形态：对象 + epg_data 数组
	body := []byte(`{"channel_id":"1","channel_name":"CCTV1","epg_data":[{"start":"00:00","end":"01:05","title":"开始"},{"start":"01:05","end":"01:49","title":"生活圈"}]}`)
	progs := parseEPGContent(body)
	if len(progs) != 2 || progs[0].Title != "开始" || progs[1].Title != "生活圈" {
		t.Fatalf("epg_data 解析不对: %+v", progs)
	}
	// XMLTV
	xml := []byte(`<tv><programme start="20260901000000 +0800" stop="20260901010000 +0800" channel="1"><title>新闻</title></programme></tv>`)
	if p := parseEPGContent(xml); len(p) != 1 || p[0].Title != "新闻" {
		t.Fatalf("XMLTV 解析不对: %+v", p)
	}
	// 未知格式 → 空
	if p := parseEPGContent([]byte("garbage")); p != nil {
		t.Fatalf("未知格式应返回 nil: %+v", func() []Program { return p }())
	}
}

// TestTxtEpgTemplateGzip txt 模板 EPG 源返回 gzip 压缩的 XMLTV（xml.gz）：
// fetchTemplateEPG 需先解压再进解析链，否则 XML/JSON 解析全失败。
func TestTxtEpgTemplateGzip(t *testing.T) {
	xmlBody := `<tv><programme start="20260901000000 +0800" stop="20260901010000 +0800" channel="1"><title>新闻联播</title></programme></tv>`
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(xmlBody)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		w.Write(buf.Bytes())
	}))
	defer up.Close()

	h := &Handler{stream: up.Client()}
	progs := h.fetchTemplateEPG(context.Background(), up.URL)
	if len(progs) != 1 || progs[0].Title != "新闻联播" {
		t.Fatalf("gzip XMLTV 模板解析不对: %+v", progs)
	}
}

func TestEPGBankNameLookup(t *testing.T) {
	b := NewEPGBank()
	xm := `<tv><channel id="CCTV10"><display-name lang="zh">CCTV10</display-name></channel>` +
		`<programme channel="CCTV10" start="20260901120000 +0800" stop="20260901130000 +0800"><title>午间新闻</title></programme></tv>`
	installXMLTV(t, b, xm)
	// 按 display-name（频道名）查
	ps := b.Programs("CCTV10", "20260901")
	if len(ps) != 1 || ps[0].Title != "午间新闻" {
		t.Fatalf("XMLTV 名称查询不对: %+v", ps)
	}
	// 非匹配日期 → 空
	if ps2 := b.Programs("CCTV10", "20260101"); len(ps2) != 0 {
		t.Fatalf("日期过滤不对: %+v", ps2)
	}
}

// TestEPGNormalizedNameLookup 归一化匹配：订阅频道名带质量后缀（4K/HD/高清）
// 而 EPG display-name 不带时，应能按归一化别名查中；归一化冲突时宁缺毋滥。
func TestEPGNormalizedNameLookup(t *testing.T) {
	b := NewEPGBank()
	xm := `<tv>` +
		`<channel id="bjws"><display-name lang="zh">北京卫视</display-name></channel>` +
		`<channel id="cctv4k"><display-name lang="zh">CCTV4K</display-name></channel>` +
		`<channel id="cctv4"><display-name lang="zh">CCTV4</display-name></channel>` +
		`<channel id="cctv"><display-name lang="zh">CCTV</display-name></channel>` +
		`<programme channel="bjws" start="20260901120000 +0800" stop="20260901130000 +0800"><title>北京新闻</title></programme>` +
		`<programme channel="cctv4k" start="20260901120000 +0800" stop="20260901130000 +0800"><title>4K 频道节目</title></programme>` +
		`</tv>`
	installXMLTV(t, b, xm)

	// 北京卫视4K（订阅带后缀）→ 匹配 EPG "北京卫视"
	ps := b.Programs("北京卫视4K", "20260901")
	if len(ps) != 1 || ps[0].Title != "北京新闻" {
		t.Fatalf("4K 后缀变体未匹配: %+v", ps)
	}
	// 带分隔符的变体 "北京卫视-4K"（去 "-" 再剥 4k → 北京卫视）同样匹配
	if psH := b.Programs("北京卫视-4K", "20260901"); len(psH) != 1 || psH[0].Title != "北京新闻" {
		t.Fatalf("带分隔符的 4K 变体未匹配: %+v", psH)
	}
	// CCTV4K 精确匹配（EPG 里是独立频道），不被 4K 剥离逻辑破坏
	ps2 := b.Programs("CCTV4K", "20260901")
	if len(ps2) != 1 || ps2[0].Title != "4K 频道节目" {
		t.Fatalf("CCTV4K 精确匹配不对: %+v", ps2)
	}
	// CCTV4K 与 CCTV 归一化冲突（均剥 4k → cctv）：宁缺毋滥，归一化键被删。
	// "CCTV4KHD" 归一到 "cctv"，应查空而非误匹配到 CCTV4K 或 CCTV。
	if ps3 := b.Programs("CCTV4KHD", "20260901"); len(ps3) != 0 {
		t.Fatalf("归一化冲突应宁缺毋滥: %+v", ps3)
	}

	// EPG 自身同时存在 "北京卫视" 与 "北京卫视4K"（归一键冲突）→ 归一化别名被删：
	// "北京卫视-4K" 查空（不错配到其中一个），精确名 "北京卫视4K" 仍精确命中自己的节目。
	bc := NewEPGBank()
	installXMLTV(t, bc, `<tv>`+
		`<channel id="bjws"><display-name lang="zh">北京卫视</display-name></channel>`+
		`<channel id="bjws4k"><display-name lang="zh">北京卫视4K</display-name></channel>`+
		`<programme channel="bjws" start="20260901120000 +0800" stop="20260901130000 +0800"><title>北京新闻</title></programme>`+
		`<programme channel="bjws4k" start="20260901120000 +0800" stop="20260901130000 +0800"><title>4K 频道节目</title></programme>`+
		`</tv>`)
	if got := bc.Programs("北京卫视-4K", "20260901"); len(got) != 0 {
		t.Fatalf("归一键冲突时应查空(宁缺毋滥): %+v", got)
	}
	if got := bc.Programs("北京卫视4K", "20260901"); len(got) != 1 || got[0].Title != "4K 频道节目" {
		t.Fatalf("精确名应命中独立 4K 频道: %+v", got)
	}
}

// TestEPGSourcChainFallback xml 型源链：主源（内嵌 x-tvg-url）失效时自动切到配置
// player.epg 的固定 XMLTV。覆盖「M3U 内嵌 EPG 挂掉 → 外部 xml.gz 接管」场景。
func TestEPGSourcChainFallback(t *testing.T) {
	no := false
	config.Cfg.HTTP.InsecureSkipVerify = &no
	config.Cfg.HTTP.DisableKeepAlives = &no

	// 主源：返回 404（失效）
	bad := httptest.NewServer(http.NotFoundHandler())
	defer bad.Close()
	// 回退源：有效 XMLTV
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<tv><channel id="CCTV1"><display-name lang="zh">CCTV1</display-name></channel>` +
			`<programme channel="CCTV1" start="20260901080000 +0800" stop="20260901090000 +0800"><title>朝闻天下</title></programme></tv>`))
	}))
	defer up.Close()

	b := NewEPGBank()
	b.Load(bad.URL, up.URL) // 先坏后好 → 应取回退源
	if !b.HaveData() {
		t.Fatalf("源链回退未生效: 主源失效后应加载回退源数据")
	}
	ps := b.Programs("CCTV1", "20260901")
	if len(ps) != 1 || ps[0].Title != "朝闻天下" {
		t.Fatalf("回退源查询不对: %+v", ps)
	}

	// 主源恢复：应重新优先主源
	b2 := NewEPGBank()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<tv><programme channel="9" start="20260901" stop="20260901"><title>主源节目</title></programme></tv>`))
	}))
	defer good.Close()
	b2.Load(good.URL, bad.URL)
	if ps := b2.Programs("9", "20260901"); len(ps) != 1 || ps[0].Title != "主源节目" {
		t.Fatalf("主源有效时应优先主源: %+v", ps)
	}
}

// TestEPGFallbackTemplate 跨类型补齐：template 主来源查询失败（空）时，用配置的
// 整份 XMLTV（xml）补齐该频道节目。
func TestEPGFallbackTemplate(t *testing.T) {
	no := false
	config.Cfg.HTTP.InsecureSkipVerify = &no
	config.Cfg.HTTP.DisableKeepAlives = &no

	// template 主来源：总是返回垃圾（解析失败 → 空）
	tpl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not epg"))
	}))
	defer tpl.Close()

	m := &Manager{
		epgSource: EPGSource{Type: "template", URL: tpl.URL},
		epgTpls:   []string{tpl.URL},
		epg:       NewEPGBank(),
	}
	installXMLTV(t, m.epg, `<tv><channel id="c1"><display-name lang="zh">CCTV1</display-name></channel>`+
		`<programme channel="c1" start="20260901080000 +0800" stop="20260901090000 +0800"><title>朝闻天下</title></programme></tv>`)
	h := &Handler{mgr: m, stream: tpl.Client()}
	progs := h.serveEPGQuery(context.Background(), "", "CCTV1", "20260901")
	if len(progs) != 1 || progs[0].Title != "朝闻天下" {
		t.Fatalf("template 主源为空时应由 xml 补齐: %+v", progs)
	}
}

// TestEPGXmlFallbackTemplate xml 主来源没有该频道节目（整份失效或频道缺失）时，
// 用配置的 template 补齐逐频道拉取。
func TestEPGXmlFallbackTemplate(t *testing.T) {
	no := false
	config.Cfg.HTTP.InsecureSkipVerify = &no
	config.Cfg.HTTP.DisableKeepAlives = &no

	// 备用 template：返回单频道 XMLTV
	tpl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<tv><programme start="20260901080000 +0800" stop="20260901090000 +0800" channel="1"><title>新闻 30 分</title></programme></tv>`))
	}))
	defer tpl.Close()

	// 主 xml 从未加载成功：epgTpls 里有模板来源可补齐
	m := &Manager{
		epgSource: EPGSource{Type: "xml", URL: "http://127.0.0.1:1/dead.xml"},
		epgTpls:   []string{tpl.URL},
		epg:       NewEPGBank(),
	}
	h := &Handler{mgr: m, stream: tpl.Client()}
	progs := h.serveEPGQuery(context.Background(), "", "CCTV1", "20260901")
	if len(progs) != 1 || progs[0].Title != "新闻 30 分" {
		t.Fatalf("xml 主源无数据时应由 template 补齐: %+v", progs)
	}

	// 反向：xml 主源已有该频道节目 → 不再打模板接口（省一次上游请求）
	var hits int32
	tpl2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Write([]byte(`<tv><programme start="20260901080000 +0800" stop="20260901090000 +0800" channel="1"><title>模板节目</title></programme></tv>`))
	}))
	defer tpl2.Close()
	m2 := &Manager{
		epgSource: EPGSource{Type: "xml", URL: "https://e.example.com/e.xml.gz"},
		epgTpls:   []string{tpl2.URL},
		epg:       NewEPGBank(),
	}
	installXMLTV(t, m2.epg, `<tv><channel id="c1"><display-name lang="zh">CCTV1</display-name></channel>`+
		`<programme channel="c1" start="20260901080000 +0800" stop="20260901090000 +0800"><title>朝闻天下</title></programme></tv>`)
	h2 := &Handler{mgr: m2, stream: tpl2.Client()}
	if progs := h2.serveEPGQuery(context.Background(), "", "CCTV1", "20260901"); len(progs) != 1 || progs[0].Title != "朝闻天下" {
		t.Fatalf("xml 有数据时应直接用 xml: %+v", progs)
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Fatalf("xml 命中时不应再请求模板源, 实际 %d 次", n)
	}
}

func TestRewriteM3U8(t *testing.T) {
	src := "https://h.example.com/live/master.m3u8"
	in := "#EXTM3U\n#EXTINF:5.0,\n/absolute/seg1.ts\n#EXTINF:5.0,\nhttps://cdn-x.example.com/other/seg.ts\n#EXTINF:5.0,\nrel/seg2.ts\n#EXT-X-ENDLIST\n"
	out, origin, tokens, err := rewrittenM3U8(strings.NewReader(in), src, "KEY")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	s := string(out)
	// 不能暴露 host/协议/长路径；每行应为绝对短路径 /player/KEY/<token>
	if strings.Contains(s, "://") || strings.Contains(s, ".ts") {
		t.Fatalf("不应暴露源地址（应为短 token）:\n%s", s)
	}
	if len(tokens) != 3 {
		t.Fatalf("token 数不对: %d", len(tokens))
	}
	wantAbs := map[string]bool{
		"https://h.example.com/absolute/seg1.ts": true,
		"https://cdn-x.example.com/other/seg.ts": true,
		"https://h.example.com/live/rel/seg2.ts": true,
	}
	found := map[string]bool{}
	for tok, abs := range tokens {
		if len(tok) != 10 {
			t.Fatalf("token 应 10 位: %q", tok)
		}
		if wantAbs[abs] {
			found[abs] = true
		}
		if !strings.Contains(s, "/player/KEY/"+tok) {
			t.Fatalf("m3u8 缺绝对 token 路径 /player/KEY/%s", tok)
		}
	}
	if len(found) != 3 {
		t.Fatalf("token 映射不全: %v / %v", found, tokens)
	}
	if origin != "https://cdn-x.example.com" {
		t.Fatalf("origin 学取不对: %q", origin)
	}
}

// TestServeCatchupPhpRtsp：php:// 与 rtsp:// 直连源（如 xxx 解析脚本、咪咕 IPTV）
// 同样支持 playseek 回看；udp/rtp 组播无时移仍拒绝。
func TestServeCatchupPhpRtsp(t *testing.T) {
	b := false
	config.Cfg.HTTP.InsecureSkipVerify = &b
	config.Cfg.HTTP.DisableKeepAlives = &b

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("爱看咪咕,#genre#\n" +
			"CCTV1,php://xxx.php?id=cctv1\n" +
			"CCTV2,rtsp://192.0.2.70/PLTV/88888888/224/3221225699/iptv8040.smil\n" +
			"CCTV3,udp://239.3.1.1:8001\n"))
	}))
	defer up.Close()

	setTestPlayer(config.PlayerConfig{Enabled: true, Subscription: up.URL}, t)
	mgr := NewManager(&config.Cfg.Player)
	mgr.httpClient = up.Client()
	mgr.Reload()

	keys := map[string]string{}
	for _, c := range mgr.Channels() {
		keys[c.Name] = c.Key
	}

	h := NewHandler(mgr)
	// from/to 用 Unix 秒（服务端本地时区换算 playseek 的 YmdHis）
	t0 := time.Date(2026, 9, 4, 12, 0, 0, 0, time.Local).Unix()
	t1 := time.Date(2026, 9, 4, 13, 0, 0, 0, time.Local).Unix()
	doCatchup := func(name string) (int, string) {
		rr := httptest.NewRecorder()
		h.ServeCatchup(rr, httptest.NewRequest("GET",
			"/api/player/catchup?key="+keys[name]+"&from="+strconv.FormatInt(t0, 10)+"&to="+strconv.FormatInt(t1, 10), nil))
		return rr.Code, rr.Body.String()
	}

	// php:// → &playseek 追加（URL 已含 query）
	code, body := doCatchup("CCTV1")
	if code != http.StatusOK {
		t.Fatalf("php 源 catchup 应 200, got %d: %s", code, body)
	}
	var resp struct {
		Play string `json:"play"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil || resp.Play == "" {
		t.Fatalf("php catchup 响应异常: %s", body)
	}
	parts := strings.SplitN(strings.TrimPrefix(resp.Play, "/player/"), "/", 2)
	if got := h.resolveToken(keys["CCTV1"], parts[1]); got != "php://xxx.php?id=cctv1&playseek=20260904120000-20260904130000" {
		t.Fatalf("php 回看地址不对: %q", got)
	}

	// rtsp:// → PLTV 换 TVOD + ?playseek
	code, body = doCatchup("CCTV2")
	if code != http.StatusOK {
		t.Fatalf("rtsp 源 catchup 应 200, got %d: %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil || resp.Play == "" {
		t.Fatalf("rtsp catchup 响应异常: %s", body)
	}
	parts = strings.SplitN(strings.TrimPrefix(resp.Play, "/player/"), "/", 2)
	if got := h.resolveToken(keys["CCTV2"], parts[1]); got != "rtsp://192.0.2.70/TVOD/88888888/224/3221225699/iptv8040.smil?playseek=20260904120000-20260904130000" {
		t.Fatalf("rtsp 回看地址不对: %q", got)
	}

	// udp:// 组播 → 仍 400
	if code, _ := doCatchup("CCTV3"); code != http.StatusBadRequest {
		t.Fatalf("udp 源 catchup 应 400, got %d", code)
	}

	// 回看 token 拉流：php:// 地址应走内嵌解释器分派（脚本缺失 → 502 php source failed），
	// 而不是落入 http 拉流报 unsupported protocol scheme。
	rr3 := httptest.NewRecorder()
	h.ServePull(rr3, httptest.NewRequest("GET", "/player/"+keys["CCTV1"]+"/"+parts[1], nil))
	if strings.Contains(rr3.Body.String(), "unsupported protocol scheme") {
		t.Fatalf("php token 不应进 http 拉流: %d %s", rr3.Code, rr3.Body.String())
	}
	if rr3.Code != http.StatusBadGateway || !strings.Contains(rr3.Body.String(), "php source failed") {
		t.Fatalf("php token 应走解释器分派(502), got %d %s", rr3.Code, rr3.Body.String())
	}
}

// TestServeHTTPRedirectCache：302 解析型源（如 gdlt.php）的最终地址应缓存，
// m3u8 刷新不再重复执行解析脚本；缓存失效时回退重新解析。
func TestServeHTTPRedirectCache(t *testing.T) {
	b := false
	config.Cfg.HTTP.InsecureSkipVerify = &b
	config.Cfg.HTTP.DisableKeepAlives = &b

	phpHits := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tv.txt":
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("央视,#genre#\nCCTV1,http://" + r.Host + "/live/gdlt.php?id=1\n"))
		case "/live/gdlt.php":
			phpHits++
			// 回看会话（带 playseek）→ 解析到不同的回看地址，用于检测缓存污染
			if r.URL.Query().Get("playseek") != "" {
				http.Redirect(w, r, "/live/catchup.m3u8", http.StatusFound)
				return
			}
			http.Redirect(w, r, "/live/1.m3u8", http.StatusFound)
		case "/live/catchup.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.Write([]byte("#EXTM3U\n#EXT-X-ENDLIST\n#EXTINF:6,\ncatchup0.ts\n"))
		case "/live/1.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.Write([]byte("#EXTM3U\n#EXTINF:6,\nseg0.ts\n"))
		case "/live/seg0.ts":
			w.Write([]byte("TS"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()

	setTestPlayer(config.PlayerConfig{Enabled: true, Subscription: up.URL + "/tv.txt"}, t)
	mgr := NewManager(&config.Cfg.Player)
	mgr.httpClient = up.Client()
	mgr.Reload()
	h := NewHandler(mgr)
	h.httpClient = up.Client()
	key := mgr.Channels()[0].Key

	// 两次 m3u8 刷新：第二次应命中缓存，不再访问解析脚本
	for i := 0; i < 2; i++ {
		rr := httptest.NewRecorder()
		h.ServePull(rr, httptest.NewRequest("GET", "/player/"+key, nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("m3u8 刷新 #%d 应 200, got %d %s", i+1, rr.Code, rr.Body.String())
		}
	}
	if phpHits != 1 {
		t.Fatalf("解析脚本应只执行 1 次, got %d", phpHits)
	}

	// 分片 token 拉流仍正常（token 基于 m3u8 重写）
	m3u8 := ""
	rr := httptest.NewRecorder()
	h.ServePull(rr, httptest.NewRequest("GET", "/player/"+key, nil))
	m3u8 = rr.Body.String()
	tok := ""
	for _, line := range strings.Split(m3u8, "\n") {
		l := strings.TrimSpace(line)
		if l != "" && !strings.HasPrefix(l, "#") {
			tok = strings.TrimPrefix(l, "/player/"+key+"/")
			break
		}
	}
	// 会话窗口超时（换台/回看/返回直播等间隔较久）→ 应重新请求解析脚本
	v, _ := h.redirects.Load(key)
	v.(*redirectCache).lastUsed = time.Now().Add(-2 * redirectActiveWindow)
	rr3 := httptest.NewRecorder()
	h.ServePull(rr3, httptest.NewRequest("GET", "/player/"+key, nil))
	if rr3.Code != http.StatusOK {
		t.Fatalf("窗口超时后的 m3u8 刷新应 200, got %d", rr3.Code)
	}
	if phpHits != 2 {
		t.Fatalf("窗口超时后应重新执行解析脚本, phpHits=%d", phpHits)
	}

	rr4 := httptest.NewRecorder()
	h.ServePull(rr4, httptest.NewRequest("GET", "/player/"+key+"/"+tok, nil))
	if rr4.Code != http.StatusOK || rr4.Body.String() != "TS" {
		t.Fatalf("分片拉流应 200 TS, got %d %s", rr4.Code, rr4.Body.String())
	}

	// 回看路径：传入的 abs 是带 playseek 的会话地址（≠ RawURL），解析成功
	// 不得写入直播缓存，否则返回直播时会命中回看缓存。
	var liveCh *Channel
	for _, c := range mgr.Channels() {
		liveCh = c
		break
	}
	rr5 := httptest.NewRecorder()
	h.serveHTTP(rr5, httptest.NewRequest("GET", "/player/"+key+"/x", nil), liveCh,
		liveCh.RawURL+"&playseek=20260905080000-20260905110815")
	if rr5.Code != http.StatusOK {
		t.Fatalf("回看拉流应 200, got %d", rr5.Code)
	}
	if got := h.getRedirect(key); got != "" && !strings.HasSuffix(got, "/live/1.m3u8") {
		t.Fatalf("回看解析污染直播缓存, got %q", got)
	}
}

// TestServeEPGKeyOnly：/api/player/epg 的 key 分支——key/ch 都缺 400、未登记 key 403、
// 合法 key 200（无 EPG 源时返回空节目单，含 name/date 回显）；date 归一容忍三种写法、空则今天。
func TestServeEPGKeyOnly(t *testing.T) {
	b := false
	config.Cfg.HTTP.InsecureSkipVerify = &b
	config.Cfg.HTTP.DisableKeepAlives = &b

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("#EXTM3U\n#EXTINF:-1 tvg-id=\"1\" tvg-name=\"CCTV1\" group-title=\"央视\",CCTV1\nhttp://192.0.2.1/live/1.m3u8\n"))
	}))
	defer up.Close()

	setTestPlayer(config.PlayerConfig{Enabled: true, Subscription: up.URL}, t)
	mgr := NewManager(&config.Cfg.Player)
	mgr.httpClient = up.Client()
	mgr.Reload()
	h := NewHandler(mgr)
	if len(mgr.Channels()) != 1 {
		t.Fatalf("频道数不符: %d", len(mgr.Channels()))
	}
	key := mgr.Channels()[0].Key

	doEPG := func(query string) (int, string) {
		rr := httptest.NewRecorder()
		h.ServeEPG(rr, httptest.NewRequest("GET", "/api/player/epg"+query, nil))
		return rr.Code, rr.Body.String()
	}

	// key / ch 都缺 → 400
	if code, body := doEPG("?date=20260918"); code != http.StatusBadRequest {
		t.Fatalf("缺 key/ch 应 400, got %d: %s", code, body)
	}
	// 未登记 key → 403
	if code, body := doEPG("?key=deadbeef"); code != http.StatusForbidden {
		t.Fatalf("未知 key 应 403, got %d: %s", code, body)
	}
	// 合法 key → 200 空节目单（回显 name/date）
	code, body := doEPG("?key=" + key + "&date=2026-09-18")
	if code != http.StatusOK {
		t.Fatalf("合法 key 应 200, got %d: %s", code, body)
	}
	var resp struct {
		Programs []Program `json:"programs"`
		Name     string    `json:"name"`
		Date     string    `json:"date"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil || resp.Programs == nil {
		t.Fatalf("epg 响应异常: %s", body)
	}
	if resp.Name != "CCTV1" || resp.Date != "20260918" {
		t.Fatalf("响应应回显 name/date: %s", body)
	}
	for _, c := range []struct{ in, want string }{
		{"2026-09-18", "20260918"},
		{"2026/09/18", "20260918"},
		{"20260918", "20260918"},
		{"", time.Now().Format("20060102")},
	} {
		if got := normalizeEPGDate(c.in); got != c.want {
			t.Fatalf("normalizeEPGDate(%q)=%q，期望 %q", c.in, got, c.want)
		}
	}
}

// TestServeEPGByNameStandard 对外标准查询：/api/player/epg?ch=<频道名>&date=<日期>
// 不要求频道在本机订阅白名单里（第三方把本机当 EPG 源用），name= 为同义参数。
func TestServeEPGByNameStandard(t *testing.T) {
	no := false
	config.Cfg.HTTP.InsecureSkipVerify = &no
	config.Cfg.HTTP.DisableKeepAlives = &no

	// 两个模板来源：A 只有 08:00 一条，B 有 08:00（重复，应被 A 压制）与 10:00 一条
	tplA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("ch"); got != "北京卫视" {
			t.Errorf("模板 A {name} 填充不对: %q", got)
		}
		w.Write([]byte(`<tv><programme start="20260901080000 +0800" stop="20260901090000 +0800" channel="1"><title>A 台节目</title></programme></tv>`))
	}))
	defer tplA.Close()
	tplB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<tv>` +
			`<programme start="20260901080000 +0800" stop="20260901090000 +0800" channel="1"><title>B 台同档节目</title></programme>` +
			`<programme start="20260901100000 +0800" stop="20260901110000 +0800" channel="1"><title>B 台独有节目</title></programme></tv>`))
	}))
	defer tplB.Close()

	tpl := "{srv}?ch={name}&date={date}"
	m := &Manager{
		epgSource: EPGSource{Type: "template", URL: strings.Replace(tpl, "{srv}", tplA.URL, 1)},
		epgTpls: []string{
			strings.Replace(tpl, "{srv}", tplA.URL, 1),
			strings.Replace(tpl, "{srv}", tplB.URL, 1),
		},
		epg: NewEPGBank(),
	}
	h := NewHandler(m)
	rr := httptest.NewRecorder()
	h.ServeEPG(rr, httptest.NewRequest("GET", "/api/player/epg?ch="+url.QueryEscape("北京卫视")+"&date=20260901", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("按名查询应 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Programs []Program `json:"programs"`
		Name     string    `json:"name"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("epg 响应异常: %s", rr.Body.String())
	}
	if resp.Name != "北京卫视" {
		t.Fatalf("name 回显不对: %q", resp.Name)
	}
	// 两个模板来源合并：08:00 取靠前来源(A)，10:00 来自 B；按时间排序
	if len(resp.Programs) != 2 || resp.Programs[0].Title != "A 台节目" || resp.Programs[1].Title != "B 台独有节目" {
		t.Fatalf("模板多来源合并不对: %+v", resp.Programs)
	}

	// name= 同义：同上能查到（改查整份 XMLTV 分支需另配来源，这里只验证参数等效）
	rr2 := httptest.NewRecorder()
	h.ServeEPG(rr2, httptest.NewRequest("GET", "/api/player/epg?name="+url.QueryEscape("北京卫视")+"&date=20260901", nil))
	if rr2.Code != http.StatusOK {
		t.Fatalf("name= 应同义, got %d: %s", rr2.Code, rr2.Body.String())
	}
}
