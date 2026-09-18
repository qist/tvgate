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

// reQualityTag 提取频道名尾部的画质/规格变体作为线路 Tag（仅展示用）。
// 只认名称结尾的连续规格词（可带空格/横线/点分隔），避免误伤中间词
// （如 "4K影院" 不算 4K 频道变体，"CCTV5 4K" 算）。
var reQualityTag = regexp.MustCompile(`(?i)(?:[\s\-_.·]*(4K|8K|UHD|FHD|1080[IiP]?|720[Pp]?|576[Pp]?|HD|超清|高清|蓝光|极清|50FPS|60FPS))+$`)

// qualityTagOf 提取频道名尾部画质/规格词作为线路 Tag（仅展示，不参与聚合；
// 组内聚合只认名称完全一致，见 aggregateIntraGroup。epg.go 的
// normalizeChannelName 是 EPG 模糊匹配用途，行为约定不同，不共用）。
func qualityTagOf(name string) string {
	if m := reQualityTag.FindStringSubmatch(strings.TrimSpace(name)); m != nil && m[1] != "" {
		return strings.ToUpper(m[1])
	}
	return ""
}

// aggregateIntraGroup 组内聚合：同分组内**名称完全一致**（仅去首尾空白）的
// 频道合并为首条频道的多线路——"北京卫视4K" 与 "北京卫视" 名称不同，是两个
// 频道，绝不合并。首条为代表（key/名称不变，TVG 字段向首条非空补齐），
// ID 绑定 分组|名称（稳定，不随线路增减变化）；Lines 首项即代表自身。
// 仅影响列表展示与线路切换；白名单 channels 已含全部线路 key，不受影响。
func aggregateIntraGroup(order []*Channel) []*Channel {
	type aggKey struct{ group, name string }
	idx := make(map[aggKey]*Channel, len(order))
	out := make([]*Channel, 0, len(order))
	for _, c := range order {
		name := strings.TrimSpace(c.Name)
		k := aggKey{c.Group, name}
		head, ok := idx[k]
		if !ok {
			idx[k] = c
			c.ID = shortHash(c.Group + "|" + name)
			c.Lines = []*LineInfo{{Key: c.Key, Tag: qualityTagOf(c.Name), Scheme: c.Scheme}}
			out = append(out, c)
			continue
		}
		// 重复频道：并入首条线路表；TVG/EPG 字段向首条非空处补齐
		head.Lines = append(head.Lines, &LineInfo{Key: c.Key, Tag: qualityTagOf(c.Name), Scheme: c.Scheme})
		if head.TVGID == "" {
			head.TVGID = c.TVGID
		}
		if head.TVGName == "" {
			head.TVGName = c.TVGName
		}
		if head.TVGLogo == "" {
			head.TVGLogo = c.TVGLogo
		}
		if head.EpgType == "none" || head.EpgType == "" {
			head.EpgType = c.EpgType
		}
	}
	return out
}
