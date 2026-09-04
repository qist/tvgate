package player

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qist/tvgate/config"
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
}
