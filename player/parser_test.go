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
