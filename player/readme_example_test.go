package player

import "testing"

// TestReadmeExamples 逐字验证 README.md「订阅格式」章节中的示例可被解析（文档与实现一致性守卫）
func TestReadmeExamples(t *testing.T) {
	// README M3U 示例（原文）
	m3u := "#EXTM3U x-tvg-url=\"https://epg.example.com/epg.xml.gz\"\n" +
		"#EXTINF:-1 tvg-id=\"CCTV1\" tvg-name=\"CCTV1\" tvg-logo=\"https://logo.example.com/CCTV1.png\" group-title=\"央视\" ua=\"okhttp/3.8.1\",CCTV1\n" +
		"http://source.example.com/cctv1.m3u8\n"
	chans, es := parseSubscription([]byte(m3u), "readme")
	if len(chans) != 1 {
		t.Fatalf("M3U: 期望 1 频道, got %d", len(chans))
	}
	c := chans[0]
	if c.Name != "CCTV1" || c.TVGID != "CCTV1" || c.TVGName != "CCTV1" ||
		c.TVGLogo != "https://logo.example.com/CCTV1.png" || c.Group != "央视" ||
		c.UA != "okhttp/3.8.1" || c.Scheme != "http" {
		t.Fatalf("M3U 字段不符: %+v", c)
	}
	if es.Type != "xml" || es.URL != "https://epg.example.com/epg.xml.gz" {
		t.Fatalf("M3U EPG 不符: %+v", es)
	}

	// README TXT 示例（原文）
	txt := "央视,#genre#\n" +
		"ua=okhttp/3.8.1\n" +
		"CCTV1,http://source.example.com/cctv1.m3u8\n" +
		"CCTV2,http://source.example.com/cctv2.m3u8,ua=Mozilla/5.0\n" +
		"epg=https://epg.example.com/?ch={name}&date={date}\n" +
		"logo=https://logo.example.com/{name}.png\n"
	chans2, es2 := parseSubscription([]byte(txt), "readme")
	if len(chans2) != 2 {
		t.Fatalf("TXT: 期望 2 频道, got %d", len(chans2))
	}
	a, b := chans2[0], chans2[1]
	if a.Name != "CCTV1" || a.Group != "央视" || a.UA != "okhttp/3.8.1" || a.Scheme != "http" || a.EpgType != "txt" {
		t.Fatalf("TXT CCTV1 不符: %+v", a)
	}
	if b.Name != "CCTV2" || b.Group != "央视" || b.UA != "Mozilla/5.0" {
		t.Fatalf("TXT 频道级 ua 覆盖不符: %+v", b)
	}
	if es2.Type != "template" || es2.URL != "https://epg.example.com/?ch={name}&date={date}" || es2.Logo != "https://logo.example.com/{name}.png" {
		t.Fatalf("TXT epg/logo 不符: %+v", es2)
	}
	t.Logf("M3U: %+v | EPG: %+v", *chans[0], es)
	t.Logf("TXT: %+v | %+v | EPG: %+v", *a, *b, es2)
}
