package player

import "testing"

func TestParseM3URTSP(t *testing.T) {
	content := []byte("#EXTM3U x-tvg-url=\"https://example/epg.xml.gz\"\n" +
		"#EXTINF:-1,tvg-id=\"1\" tvg-name=\"CCTV1\" tvg-logo=\"l.png\" group-title=\"央视\",CCTV1\n" +
		"rtsp://139.215.98.88/PLTV/8888/224/ab.smil\n" +
		"#EXTINF:-1 group-title=\"卫视\",上海卫视\n" +
		"http://119.2.255.6:9091/live/1001.m3u8\n")
	chans, es := parseSubscription(content, "sub")
	if len(chans) != 2 {
		t.Fatalf("期望 2 频道, got %d", len(chans))
	}
	if chans[0].Scheme != "rtsp" || chans[0].Key != "" || chans[0].TVGID != "1" || chans[0].Group != "央视" {
		t.Fatalf("rtsp 频道解析不对: %+v", chans[0])
	}
	if es.Type != "xml" || es.URL != "https://example/epg.xml.gz" {
		t.Fatalf("M3U epg 解析不对: %+v", es)
	}
	if chans[1].Scheme != "http" {
		t.Fatalf("http 频道解析不对: %+v", chans[1])
	}
}

func TestParseTXT(t *testing.T) {
	content := []byte("爱看咪咕,#genre#\nCCTV1,https://a/b.php?id=cctv1\nCCTV2,https://a/b.php?id=cctv2\n\n" +
		"epg=https://epg.<your-domain>/?ch={name}&date={date}\n" +
		"logo=https://logo.<your-domain>/{name}.png\n" +
		"谷豆,#genre#\nCCTV1,http://119.2.255.6:9091/live/1001.m3u8\n")
	chans, es := parseSubscription(content, "sub")
	if len(chans) != 3 {
		t.Fatalf("期望 3 频道, got %d", len(chans))
	}
	if chans[0].Group != "爱看咪咕" {
		t.Fatalf("group 解析不对: %+v", chans[0])
	}
	if es.Type != "template" || es.URL != "https://epg.<your-domain>/?ch={name}&date={date}" {
		t.Fatalf("txt epg 解析不对: %+v", es)
	}
	if es.Logo != "https://logo.<your-domain>/{name}.png" {
		t.Fatalf("txt logo 解析不对: %+v", es)
	}
}

func TestParseTXTUA(t *testing.T) {
	// 行尾 ua= 优先；组级 ua= 行作用于所在分组的后续频道；
	// 进入新分组时重置（未配置 ua= 的分组回落 player.ua 全局默认，即空）
	content := []byte("蜀小果,#genre#\n" +
		"ua=Mozilla/5.0 (Windows NT 10.0; Win64; x64) Edg/152\n" +
		"峨眉电影4K,http://192.0.2.1/live/xxx.php?id=emdy4k\n" +
		"CCTV1,http://192.0.2.1/live/xxx.php?id=cctv1\n" +
		"特例,http://192.0.2.1/live/yyy.php?id=cctv2,ua=okhttp/3.8.1\n" +
		"百视通,#genre#\n" +
		"CCTV3,http://192.0.2.1/live/yyy.php?id=cctv3\n")
	chans, _ := parseSubscription(content, "sub")
	if len(chans) != 4 {
		t.Fatalf("期望 4 频道, got %d", len(chans))
	}
	if chans[0].UA != "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Edg/152" {
		t.Fatalf("组级 ua= 未生效: %+v", chans[0])
	}
	if chans[1].UA != "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Edg/152" {
		t.Fatalf("组级 ua= 未延续到组内后续频道: %+v", chans[1])
	}
	if chans[2].UA != "okhttp/3.8.1" {
		t.Fatalf("行尾 ,ua= 应覆盖组级: %+v", chans[2])
	}
	if chans[3].UA != "" {
		t.Fatalf("新分组未配置 ua= 应重置为空(回落全局默认): %+v", chans[3])
	}
}

func TestParseM3UUA(t *testing.T) {
	content := []byte("#EXTM3U\n" +
		"#EXTINF:-1 tvg-id=\"1\" ua=\"okhttp/3.8.1\",CCTV1\n" +
		"http://x/live/a.m3u8\n" +
		"#EXTINF:-1,CCTV2\n" +
		"http://x/live/b.m3u8\n")
	chans, _ := parseSubscription(content, "sub")
	if len(chans) != 2 {
		t.Fatalf("期望 2 频道, got %d", len(chans))
	}
	if chans[0].UA != "okhttp/3.8.1" {
		t.Fatalf("M3U ua 属性解析不对: %+v", chans[0])
	}
	if chans[1].UA != "" {
		t.Fatalf("无 ua 的 M3U 频道 UA 应为空: %+v", chans[1])
	}
}

func TestParseTXTProxyRefererOrigin(t *testing.T) {
	// proxy=/referer=/origin= 与 ua= 并列：组/文件级默认作用于后续频道，进入新分组重置；
	// 频道行可用 `,key=value` 覆盖，proxy= 值可含逗号（约定放行尾）。
	content := []byte("蜀小果,#genre#\n" +
		"proxy=socks5://127.0.0.1:7890,http://127.0.0.1:8080\n" +
		"referer=https://v.example.com/\n" +
		"origin=https://v.example.com\n" +
		"CCTV1,http://192.0.2.1/live/a.m3u8\n" +
		"CCTV2,http://192.0.2.1/live/b.m3u8,proxy=socks5://192.0.2.9:1080\n" +
		"CCTV3,http://192.0.2.1/live/c.m3u8,referer=https://b.example.com/,origin=https://b.example.com,proxy=http://192.0.2.10:3128\n" +
		"百视通,#genre#\n" +
		"CCTV4,http://192.0.2.1/live/d.m3u8\n")
	chans, _ := parseSubscription(content, "sub")
	if len(chans) != 4 {
		t.Fatalf("期望 4 频道, got %d", len(chans))
	}
	// 组级默认：多代理 + Referer/Origin
	if len(chans[0].Proxies) != 2 || chans[0].Proxies[0] != "socks5://127.0.0.1:7890" || chans[0].Proxies[1] != "http://127.0.0.1:8080" {
		t.Fatalf("组级 proxy 未生效: %+v", chans[0].Proxies)
	}
	if chans[0].Referer != "https://v.example.com/" || chans[0].Origin != "https://v.example.com" {
		t.Fatalf("组级 referer/origin 未生效: %+v", chans[0])
	}
	// 频道级 proxy 覆盖，referer/origin 继承组级
	if len(chans[1].Proxies) != 1 || chans[1].Proxies[0] != "socks5://192.0.2.9:1080" {
		t.Fatalf("频道级 proxy 未覆盖: %+v", chans[1].Proxies)
	}
	if chans[1].Referer != "https://v.example.com/" {
		t.Fatalf("频道级未配置 referer 应继承组级: %+v", chans[1])
	}
	// 频道级 referer/origin/proxy 全部覆盖
	if chans[2].Referer != "https://b.example.com/" || chans[2].Origin != "https://b.example.com" {
		t.Fatalf("频道级 referer/origin 未覆盖: %+v", chans[2])
	}
	if len(chans[2].Proxies) != 1 || chans[2].Proxies[0] != "http://192.0.2.10:3128" {
		t.Fatalf("频道级 proxy 未覆盖: %+v", chans[2].Proxies)
	}
	// 新分组重置：未配置回落空
	if len(chans[3].Proxies) != 0 || chans[3].Referer != "" || chans[3].Origin != "" {
		t.Fatalf("新分组应重置 proxy/referer/origin: %+v", chans[3])
	}
}

func TestParseM3UProxyRefererOrigin(t *testing.T) {
	content := []byte("#EXTM3U\n" +
		"#EXTINF:-1 proxy=\"socks5://127.0.0.1:7890,http://127.0.0.1:8080\" referer=\"https://v.example.com/\" origin=\"https://v.example.com\",CCTV1\n" +
		"http://x/live/a.m3u8\n" +
		"#EXTINF:-1,CCTV2\n" +
		"http://x/live/b.m3u8\n")
	chans, _ := parseSubscription(content, "sub")
	if len(chans) != 2 {
		t.Fatalf("期望 2 频道, got %d", len(chans))
	}
	if len(chans[0].Proxies) != 2 || chans[0].Referer != "https://v.example.com/" || chans[0].Origin != "https://v.example.com" {
		t.Fatalf("M3U proxy/referer/origin 属性解析不对: %+v", chans[0])
	}
	if len(chans[1].Proxies) != 0 || chans[1].Referer != "" || chans[1].Origin != "" {
		t.Fatalf("无属性频道应为空: %+v", chans[1])
	}
}

func TestResolveSub(t *testing.T) {
	abs, ok := resolveSub("https://a.com/live/master.m3u8?token=1", "dir/seg1.ts")
	if !ok || abs != "https://a.com/live/dir/seg1.ts" {
		t.Fatalf("同源子路径解析不对: %q %v", abs, ok)
	}
	// 跨源（绝对 URL 换 host）拒绝
	if _, ok := resolveSub("https://a.com/live/master.m3u8", "https://evil.com/x.ts"); ok {
		t.Fatal("跨源子路径应被拒")
	}
	// scheme 注入拒绝
	if _, ok := resolveSub("https://a.com/live/master.m3u8", "javascript:alert(1)"); ok {
		t.Fatal("scheme 注入应被拒")
	}
}

func TestQualityTagOf(t *testing.T) {
	cases := []struct{ in, wantTag string }{
		{"CCTV5", ""},
		{"cctv-5", ""},
		{"CCTV5 4K", "4K"},
		{"CCTV-5-4k", "4K"},
		{"北京卫视4K", "4K"},
		{"北京卫视 高清", "高清"}, // 中文规格保留原样展示
		{"CCTV5+", ""},      // 频道号语义 + 不剥
		{"4K影院", ""},        // 中间词不误伤
		{"凤凰中文_60fps", "60FPS"},
	}
	for _, c := range cases {
		if tag := qualityTagOf(c.in); tag != c.wantTag {
			t.Fatalf("%q: got %q want %q", c.in, tag, c.wantTag)
		}
	}
}

func TestAggregateIntraGroup(t *testing.T) {
	mk := func(key, name, group, tvgID string) *Channel {
		return &Channel{Key: key, Name: name, Group: group, TVGID: tvgID, EpgType: "m3u"}
	}
	order := []*Channel{
		mk("k1", "CCTV5", "iptv", ""),
		mk("k2", "CCTV5 4K", "iptv", "cctv5-4k"), // 名称不同（画质后缀）→ 独立频道
		mk("k3", "CCTV5", "iptv", "cctv5"),       // 与 k1 名称完全一致 → 并入线路
		mk("k4", "cctv-5", "iptv", ""),           // 名称不同（大小写/分隔符）→ 独立频道
		mk("k5", "CCTV5", "移动", ""),              // 不同组不合并
		mk("k6", "CCTV6", "iptv", ""),
	}
	got := aggregateIntraGroup(order)
	// 6 条输入 → 5 条输出：CCTV5(iptv,2线路)、CCTV5 4K、cctv-5、CCTV5(移动)、CCTV6
	if len(got) != 5 {
		t.Fatalf("聚合后应 5 条，实际 %d", len(got))
	}
	head := got[0]
	if head.Key != "k1" || head.ID == "" || len(head.Lines) != 2 {
		t.Fatalf("CCTV5 应聚合 2 线路: %+v", head)
	}
	// 线路顺序
	if head.Lines[0].Key != "k1" || head.Lines[1].Key != "k3" {
		t.Fatalf("线路表不对: %+v", head.Lines)
	}
	// TVG 向首条非空补齐
	if head.TVGID != "cctv5" {
		t.Fatalf("TVGID 应补齐: %q", head.TVGID)
	}
	// 名称不一致的频道不并入，各自独立（线路 Tag 仍可提取用于展示）
	if got[1].Name != "CCTV5 4K" || len(got[1].Lines) != 1 || got[1].Lines[0].Tag != "4K" {
		t.Fatalf("CCTV5 4K 不应并入 CCTV5: %+v", got[1])
	}
	if got[2].Name != "cctv-5" || len(got[2].Lines) != 1 {
		t.Fatalf("cctv-5 不应并入 CCTV5: %+v", got[2])
	}
	// 频道 ID 稳定性：同组同名不同输入顺序 → 同 ID
	id2 := aggregateIntraGroup([]*Channel{mk("k3", "CCTV5", "iptv", ""), mk("k1", "CCTV5", "iptv", "")})[0].ID
	if id2 != head.ID {
		t.Fatalf("ID 不稳定: %q vs %q", id2, head.ID)
	}
	// 不同组 ID 必不同
	mob := got[3]
	if mob.ID == head.ID || mob.Group != "移动" || len(mob.Lines) != 1 {
		t.Fatalf("跨组不应合并: %+v", mob)
	}
}
