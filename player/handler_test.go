package player

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
// 的固定 XMLTV 自动并入回退链（epgURLs = [内嵌, 配置]），内嵌失效后由 EPGBank 切换；
// 内嵌为 template 时则配置固定 XMLTV 作为跨类型 epgBak。
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
	bak := mgr.epgBak
	mgr.mu.RUnlock()
	if len(urls) != 2 || urls[0] != inner || urls[1] != "https://example.com/e.xml.gz" {
		t.Fatalf("xml 源链组装不对: %v", urls)
	}
	if bak.Type != "none" {
		t.Fatalf("同型回退不应设置 epgBak: %+v", bak)
	}

	// 内嵌为 template、配置固定 XMLTV → epgBak 跨类型回退
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
	bak2 := mgr2.epgBak
	urls2 := append([]string(nil), mgr2.epgURLs...)
	mgr2.mu.RUnlock()
	if bak2.Type != "xml" || bak2.URL != "https://example.com/e.xml.gz" {
		t.Fatalf("template 主源 + 配置固定 XMLTV 应设 epgBak: %+v", bak2)
	}
	if len(urls2) != 0 {
		t.Fatalf("主为 template 时不应有 xml 源链: %v", urls2)
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
	b.parse([]byte(xm))
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
	b.parse([]byte(xm))

	// 北京卫视4K（订阅带后缀）→ 匹配 EPG "北京卫视"
	ps := b.Programs("北京卫视4K", "20260901")
	if len(ps) != 1 || ps[0].Title != "北京新闻" {
		t.Fatalf("4K 后缀变体未匹配: %+v", ps)
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

// TestEPGFallbackTemplate 查询级回退：template 主来源查询失败（空）时，用配置的
// 固定 XMLTV（xml）兜底查询 EPGBank。
func TestEPGFallbackTemplate(t *testing.T) {
	no := false
	config.Cfg.HTTP.InsecureSkipVerify = &no
	config.Cfg.HTTP.DisableKeepAlives = &no

	// template 主来源：总是返回垃圾（解析失败 → 空）
	tpl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not epg"))
	}))
	defer tpl.Close()
	// xml 回退源：有效
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<tv><channel id="c1"><display-name lang="zh">CCTV1</display-name></channel>` +
			`<programme channel="c1" start="20260901080000 +0800" stop="20260901090000 +0800"><title>朝闻天下</title></programme></tv>`))
	}))
	defer up.Close()

	m := &Manager{
		epgSource: EPGSource{Type: "template", URL: tpl.URL},
		epgBak:    EPGSource{Type: "xml", URL: up.URL},
		epg:       NewEPGBank(),
	}
	m.epg.parse([]byte(`<tv><channel id="c1"><display-name lang="zh">CCTV1</display-name></channel>` +
		`<programme channel="c1" start="20260901080000 +0800" stop="20260901090000 +0800"><title>朝闻天下</title></programme></tv>`))
	h := &Handler{mgr: m, stream: tpl.Client()}
	progs := h.serveEPGQuery(context.Background(), "", "CCTV1", "20260901")
	if len(progs) != 1 || progs[0].Title != "朝闻天下" {
		t.Fatalf("template 主源失效应回退 xml 兜底: %+v", progs)
	}
}

// TestEPGXmlFallbackTemplate xml 主来源整份失效（HaveData=false）时，用配置的
// template 兜底逐频道拉取。
func TestEPGXmlFallbackTemplate(t *testing.T) {
	no := false
	config.Cfg.HTTP.InsecureSkipVerify = &no
	config.Cfg.HTTP.DisableKeepAlives = &no

	// 备用 template：返回单频道 XMLTV
	tpl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<tv><programme start="20260901080000 +0800" stop="20260901090000 +0800" channel="1"><title>新闻 30 分</title></programme></tv>`))
	}))
	defer tpl.Close()

	m := &Manager{epgSource: EPGSource{Type: "xml", URL: "http://127.0.0.1:1/dead.xml"}, epg: NewEPGBank()}
	// 主 xml 从未加载成功：epgBak 为 template
	m.epgBak = EPGSource{Type: "template", URL: tpl.URL}
	h := &Handler{mgr: m, stream: tpl.Client()}
	progs := h.serveEPGQuery(context.Background(), "", "CCTV1", "20260901")
	if len(progs) != 1 || progs[0].Title != "新闻 30 分" {
		t.Fatalf("xml 主源失效应回退 template 兜底: %+v", progs)
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

// TestServeCatchupPhpRtsp：php:// 与 rtsp:// 直连源（如 akmg 解析脚本、咪咕 IPTV）
// 同样支持 playseek 回看；udp/rtp 组播无时移仍拒绝。
func TestServeCatchupPhpRtsp(t *testing.T) {
	b := false
	config.Cfg.HTTP.InsecureSkipVerify = &b
	config.Cfg.HTTP.DisableKeepAlives = &b

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("爱看咪咕,#genre#\n" +
			"CCTV1,php://akmg.php?id=cctv1\n" +
			"CCTV2,rtsp://115.153.245.70/PLTV/88888888/224/3221225699/iptv8040.smil\n" +
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
	doCatchup := func(name string) (int, string) {
		rr := httptest.NewRecorder()
		h.ServeCatchup(rr, httptest.NewRequest("GET",
			"/api/player/catchup?key="+keys[name]+"&start=20260904120000&end=20260904130000", nil))
		return rr.Code, rr.Body.String()
	}

	// php:// → &playseek 追加（URL 已含 query）
	code, body := doCatchup("CCTV1")
	if code != http.StatusOK {
		t.Fatalf("php 源 catchup 应 200, got %d: %s", code, body)
	}
	var resp struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil || resp.URL == "" {
		t.Fatalf("php catchup 响应异常: %s", body)
	}
	parts := strings.SplitN(strings.TrimPrefix(resp.URL, "/player/"), "/", 2)
	if got := h.resolveToken(keys["CCTV1"], parts[1]); got != "php://akmg.php?id=cctv1&playseek=20260904120000-20260904130000" {
		t.Fatalf("php 回看地址不对: %q", got)
	}

	// rtsp:// → PLTV 换 TVOD + ?playseek
	code, body = doCatchup("CCTV2")
	if code != http.StatusOK {
		t.Fatalf("rtsp 源 catchup 应 200, got %d: %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil || resp.URL == "" {
		t.Fatalf("rtsp catchup 响应异常: %s", body)
	}
	parts = strings.SplitN(strings.TrimPrefix(resp.URL, "/player/"), "/", 2)
	if got := h.resolveToken(keys["CCTV2"], parts[1]); got != "rtsp://115.153.245.70/TVOD/88888888/224/3221225699/iptv8040.smil?playseek=20260904120000-20260904130000" {
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
