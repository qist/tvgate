package player

import (
	"bytes"
	"regexp"
	"strings"
)

// parseSubscription 根据内容识别 M3U 或逗号 TXT，返回频道列表与 EPG 来源。
func parseSubscription(content []byte, src string) ([]*Channel, EPGSource) {
	trimmed := bytes.TrimLeft(content, "\xef\xbb\xbf \t\r\n") // 去 BOM/空白
	if bytes.HasPrefix(trimmed, []byte("#EXTM3U")) {
		return parseM3U(content)
	}
	return parseTXT(content, src)
}

var reURLTag = regexp.MustCompile(`(x-tvg-url|url-tvg)="([^"]+)"`)

func parseM3U(content []byte) ([]*Channel, EPGSource) {
	var chans []*Channel
	es := EPGSource{Type: "none"}
	var pending *Channel
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#EXTM3U") {
			if m := reURLTag.FindStringSubmatch(line); m != nil {
				es = EPGSource{Type: "xml", URL: m[2]}
			}
			continue
		}
		if strings.HasPrefix(line, "#EXTINF:") {
			pending = parseEXTINF(line)
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		// URL 行
		if pending != nil {
			pending.RawURL = strings.TrimSpace(line)
			pending.Scheme = schemeOf(pending.RawURL)
			if pending.Scheme != "" {
				pending.EpgType = "m3u"
				chans = append(chans, pending)
			}
			pending = nil
		}
	}
	return chans, es
}

// parseEXTINF 解析 #EXTINF 行的属性与名称。
func parseEXTINF(line string) *Channel {
	body := strings.TrimPrefix(line, "#EXTINF:")
	// 取最后一个逗号后的名称
	idx := strings.LastIndex(body, ",")
	name := ""
	attrs := ""
	if idx >= 0 {
		attrs = body[:idx]
		name = strings.TrimSpace(body[idx+1:])
	} else {
		attrs = body
	}
	c := &Channel{Name: name}
	c.TVGID = attrValue(attrs, "tvg-id")
	c.TVGName = attrValue(attrs, "tvg-name")
	c.TVGLogo = attrValue(attrs, "tvg-logo")
	c.Group = attrValue(attrs, "group-title")
	c.UA = attrValue(attrs, "ua")
	if c.TVGName == "" {
		c.TVGName = c.TVGID
	}
	return c
}

func attrValue(attrs, key string) string {
	re := regexp.MustCompile(key + `="([^"]*)"`)
	if m := re.FindStringSubmatch(attrs); m != nil {
		return m[1]
	}
	return ""
}

// parseTXT 解析「分组,#genre#」+「名称,URL」的逗号清单。
// EPG 通过模板行提供（含 {name} 或 {date} 占位符），形如 epg=…/epg:…/#epg=…
func parseTXT(content []byte, src string) ([]*Channel, EPGSource) {
	var chans []*Channel
	es := EPGSource{Type: "none"}
	group := ""
	curUA := "" // 当前生效的组/文件级 UA（ua= 行设置，作用于后续频道；空 = 回落 player.ua 默认）
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			line = strings.TrimPrefix(line, "#")
		}
		if strings.HasSuffix(line, "#genre#") {
			group = strings.Trim(strings.TrimSuffix(line, "#genre#"), " ,")
			// 组边界重置 UA：ua= 只作用于所在分组，未配置的分组回落 player.ua 全局默认，
			// 避免上一个分组的 ua= 泄漏到未配置 UA 的后续分组
			curUA = ""
			continue
		}
		// EPG：epg=... 含占位符 {name}/{date} → template；否则若为 http 固定文件 → xml（整份 XMLTV）
		if strings.HasPrefix(line, "epg") {
			if eq := strings.Index(line, "="); eq >= 0 {
				val := strings.TrimSpace(line[eq+1:])
				if val == "" {
					continue
				}
				if strings.Contains(val, "{") {
					es.Type = "template"
					es.URL = val
				} else if strings.HasPrefix(val, "http") {
					es.Type = "xml"
					es.URL = val
				}
				continue
			}
		}
		// 台标模板：logo=... 且含 {name}
		if strings.HasPrefix(line, "logo") && strings.Contains(line, "{name}") {
			if eq := strings.Index(line, "="); eq >= 0 {
				es.Logo = strings.TrimSpace(line[eq+1:])
				continue
			}
		}
		// 组/文件级默认 UA：独立 `ua=xxx` 行，作用于后续所有频道（再次出现覆盖；ua= 空值恢复默认）。
		if strings.HasPrefix(line, "ua=") {
			curUA = strings.TrimSpace(strings.TrimPrefix(line, "ua="))
			continue
		}
		// 每频道可选 UA：`名称,URL,ua=okhttp/3.8.1`（行尾 ua= 段，优先于组级 ua=）
		cUA := curUA
		if i := strings.LastIndex(line, ",ua="); i >= 0 {
			cUA = strings.TrimSpace(line[i+4:])
			line = line[:i]
		}
		comma := strings.LastIndex(line, ",")
		if comma <= 0 {
			continue
		}
		name := strings.TrimSpace(line[:comma])
		u := strings.TrimSpace(line[comma+1:])
		sch := schemeOf(u)
		if sch == "" || name == "" {
			continue
		}
		chans = append(chans, &Channel{
			Name:    name,
			Group:   group,
			Scheme:  sch,
			RawURL:  u,
			UA:      cUA,
			EpgType: "txt",
		})
	}
	return chans, es
}

func schemeOf(u string) string {
	for _, s := range []string{"udp://", "rtp://", "rtsp://", "php://", "https://", "http://"} {
		if strings.HasPrefix(u, s) {
			return strings.TrimSuffix(s, "://")
		}
	}
	return ""
}
