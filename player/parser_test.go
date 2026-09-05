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
		"峨眉电影4K,http://192.168.100.1/live/xg.php?id=emdy4k\n" +
		"CCTV1,http://192.168.100.1/live/xg.php?id=cctv1\n" +
		"特例,http://192.168.100.1/live/ahbst.php?id=cctv2,ua=okhttp/3.8.1\n" +
		"百视通,#genre#\n" +
		"CCTV3,http://192.168.100.1/live/ahbst.php?id=cctv3\n")
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
