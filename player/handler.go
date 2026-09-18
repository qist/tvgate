package player

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qist/tvgate/auth"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/handler"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/monitor"
	"github.com/qist/tvgate/php"
	"github.com/qist/tvgate/stream"
	httpclient "github.com/qist/tvgate/utils/http"
)

// Handler 提供播放器相关 HTTP 端点。
type Handler struct {
	mgr        *Manager
	httpClient *http.Client // 订阅/EPG/JSON 用（不跟随重定向）
	stream     *http.Client // 播放器上游用：服务端跟随重定向，避免 302 Location 回流到浏览器泄露源地址
	// segOrigins: 每个频道的分片 CDN origin（scheme://host），由 m3u8 重写时学到，供 /player/<key>/<rel> 回拉。
	segOrigins sync.Map // key -> *segOrigin
	// segGroups: 每个频道最近一次成功使用的代理组。分片 CDN 常是规则覆盖不到的 IP/内网
	// 地址，需沿用与播放列表相同的代理出口（会话/CDN 亲和）。
	segGroups sync.Map // key -> *segGroup
	// resources: 每个频道的「短 token -> 真实上游 URL」（m3u8 重写时登记），前端只见短令牌。
	resources sync.Map // key -> *resMap
	// redirects: 每个频道 302 解析型源（如 gdlt.php）的最终地址缓存。m3u8 刷新
	// 直接访问真实源，不再每次重复执行解析脚本；失效时回退重新解析。
	redirects sync.Map // key -> *redirectCache
}

// redirectCache 记录某频道解析型源的最终拉流地址与最近使用时刻。
// 仅活跃会话（连续轮询间隔内）复用：换台/回看/返回直播等间隔较久的访问重新解析。
type redirectCache struct {
	finalURL string
	lastUsed time.Time
}

// segGroup 记录某频道的代理组，带过期时间（超时后重新按域名规则匹配，跟进配置变更）。
type segGroup struct {
	pg    *config.ProxyGroupConfig
	until time.Time
}

// segOrigin 记录某频道的分片源，带过期时间。
type segOrigin struct {
	origin string
	until  time.Time
}

// resMap 记录某频道的 token->上游URL 映射，带过期时间。
// 同一频道（key）会被多个并发请求读写（多个观众/切台/分片+清单并发），
// 必须用 mu 保护 m，否则并发 map 写会触发 runtime "fatal error: concurrent map writes" 直接崩溃进程。
type resMap struct {
	mu    sync.Mutex
	m     map[string]string
	until time.Time
}

// m3u8URIRe 匹配 m3u8 标签行中的内嵌 URI 属性（EXT-X-MEDIA / EXT-X-KEY / EXT-X-MAP 等）。
var m3u8URIRe = regexp.MustCompile(`URI="([^"]+)"`)

func NewHandler(mgr *Manager) *Handler {
	sc := httpclient.NewHTTPClient(&config.Cfg, nil)
	// 播放器上游：服务端跟随重定向（最多 10 次），避免 302 Location 回流到浏览器泄露源地址；
	// 且不设整体超时（流媒体长连接）
	sc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		// 剥离 Referer：Go 会自动把上一跳 URL 设为 Referer，
		// 带 Referer 访问部分 CDN（如腾讯云直播防盗链）会 403，且泄露中间解析链
		req.Header.Del("Referer")
		return nil
	}
	sc.Timeout = 0
	return &Handler{
		mgr:        mgr,
		httpClient: httpclient.NewHTTPClient(&config.Cfg, nil),
		stream:     sc,
	}
}

// registerClient 登记活跃连接（与其它流媒体入口一致），返回 connID。
func (h *Handler) registerClient(r *http.Request, typ string) (string, func()) {
	clientIP := monitor.GetClientIP(r)
	connID := clientIP + "_" + fmt.Sprintf("%d", time.Now().UnixNano())
	monitor.ActiveClients.Register(connID, &monitor.ClientConnection{
		IP:             clientIP,
		URL:            r.URL.Path,
		UserAgent:      r.UserAgent(),
		ConnectionType: typ,
		ConnectedAt:    time.Now(),
		LastActive:     time.Now(),
	})
	return connID, func() { monitor.ActiveClients.Unregister(connID, typ) }
}

// requireToken 校验全局 token（与 /jx、/udp、/rtsp 一致）。
func (h *Handler) requireToken(w http.ResponseWriter, r *http.Request) bool {
	gt := auth.GetGlobalTokenManager()
	if gt == nil {
		return true
	}
	token := r.URL.Query().Get(gt.TokenParamName)
	clientIP := monitor.GetClientIP(r)
	connID := clientIP + "_" + md5sum(r.URL.Path)
	if !gt.ValidateToken(token, r.URL.Path, connID) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return false
	}
	gt.KeepAlive(token, connID, clientIP, r.URL.Path)
	return true
}

func md5sum(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

// ServeChannels GET /api/player/channels → 频道列表（含 key/tvg 属性）。
func (h *Handler) ServeChannels(w http.ResponseWriter, r *http.Request) {
	if !h.requireToken(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	chans := h.mgr.Channels()
	if chans == nil {
		chans = []*Channel{}
	}
	writeJSON(w, map[string]interface{}{
		"list":      chans,
		"epgSource": h.mgr.EPGSource(),
	})
}

// ServeEPG GET /api/player/epg → 节目单。两种定位方式：
//
//	key=<不透明频道key>  播放器内部用法：与 /player/<key> 同源，服务端内部换算出
//	                     tvg-id 与显示名再查 EPG，前端/第三方无需知道订阅格式；未登记 → 403。
//	ch=<频道名或tvg-id>  对外标准用法（如其它播放器把本机当 EPG 源：
//	                     epg=http://<本机>/api/player/epg?ch={name}&date={date}）；
//	                     name= 为同义参数。按名字查，**不要求**是本机订阅里的频道。
//
// date 可省略（默认今天），容忍 YYYY-MM-DD / YYYY/MM/DD / YYYYMMDD 三种写法。
// 响应 {"programs":[{from,to,title}],"name":<查询名>,"date":<YYYYMMDD>}。
//
// 数据来源见 serveEPGQuery：xml 型来源合并查询本地 EPGBank；template 型来源由服务端
// 填 {name}/{date} 后拉取（规避前端跨域 CORS）；两类互为补齐。
func (h *Handler) ServeEPG(w http.ResponseWriter, r *http.Request) {
	if !h.requireToken(w, r) {
		return
	}
	q := r.URL.Query()
	key := strings.TrimSpace(q.Get("key"))
	name := strings.TrimSpace(q.Get("ch"))
	if name == "" {
		name = strings.TrimSpace(q.Get("name"))
	}
	if key == "" && name == "" {
		http.Error(w, "key or ch required", http.StatusBadRequest)
		return
	}
	// key 优先：能被 /player/<key> 播放的频道，其 tvg-id/名称由服务端换算
	var tvgID, chanName string
	if key != "" {
		ch := h.mgr.GetByKey(key)
		if ch == nil {
			http.Error(w, "channel not found", http.StatusForbidden)
			return
		}
		tvgID, chanName = ch.TVGID, ch.Name
	} else {
		chanName = name
	}
	date := normalizeEPGDate(q.Get("date"))
	progs := h.serveEPGQuery(r.Context(), tvgID, chanName, date)
	if progs == nil {
		progs = []Program{}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	writeJSON(w, map[string]interface{}{"programs": progs, "name": chanName, "date": date})
}

// normalizeEPGDate 归一日期：容忍 YYYY-MM-DD / YYYY/MM/DD / YYYYMMDD，空则取今天（本地时区）。
func normalizeEPGDate(date string) string {
	d := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, date)
	if len(d) == 8 {
		return d
	}
	return time.Now().Format("20060102")
}

// serveEPGQuery 查某频道某天的节目单（多来源合并）：
// 主来源类型决定首选路径，另一类型（若配了）在首选**没查到该频道节目**时补齐——
// 不同 EPG 服务覆盖的频道往往不同，互补比"整体切换"实用。
//
//   - 主来源为 template：先把全部模板来源按序请求合并（{name}/{date} 填充），
//     全部为空再用整份 XMLTV 库（EPGBank）补齐；
//   - 主来源为 xml：先查 EPGBank（内部已合并全部 xml 来源），该频道为空时再用
//     模板来源补齐（xml 从未加载成功 / 该频道不在 xml 里，都走这条）。
func (h *Handler) serveEPGQuery(ctx context.Context, ch, name, date string) []Program {
	es := h.mgr.EPGSource()
	tpls := h.mgr.EPGTemplates()
	if es.Type == "template" && len(tpls) > 0 && name != "" {
		if progs := h.mergeTemplateEPGs(ctx, tpls, name, date); len(progs) > 0 {
			return progs
		}
		return h.bankPrograms(ch, name, date)
	}
	progs := h.bankPrograms(ch, name, date)
	if len(progs) == 0 && name != "" && len(tpls) > 0 {
		progs = h.mergeTemplateEPGs(ctx, tpls, name, date)
	}
	return progs
}

// bankPrograms 查本地 EPGBank（整份 XMLTV 来源，已按频道合并多份数据）。
func (h *Handler) bankPrograms(ch, name, date string) []Program {
	q := ch
	if q == "" {
		q = name
	}
	if q == "" {
		return nil
	}
	return h.mgr.EPG().Programs(q, date)
}

// mergeTemplateEPGs 按优先级逐个查询模板来源并合并结果：同一开始时间（from）已由
// 靠前来源提供则忽略后面的（避免同一时段重影），不同时间追加；最终按开始时间排序。
// 单个来源失败（拉取/解析失败返回空）只跳过自身，其余来源照常。
func (h *Handler) mergeTemplateEPGs(ctx context.Context, tpls []string, name, date string) []Program {
	var out []Program
	seen := make(map[string]bool, 16)
	for _, tpl := range tpls {
		for _, p := range h.fetchTemplateEPG(ctx, fillEpgURL(tpl, name, date)) {
			if seen[p.Start] {
				continue
			}
			seen[p.Start] = true
			out = append(out, p)
		}
	}
	if len(out) > 1 {
		sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	}
	return out
}

// fillEpgURL 把 EPG 模板里的 {name}/{date} 占位符填充为实际值（name 用 URL 转义，date 原样）。
func fillEpgURL(tpl, name, date string) string {
	u := strings.ReplaceAll(tpl, "{name}", url.PathEscape(name))
	return strings.ReplaceAll(u, "{date}", date)
}

// fetchTemplateEPG 服务端拉取 txt 模板 EPG（规避 CORS），尽量解析 XMLTV <programme> 或 JSON。
// 跟随 3xx 重定向（getEPG）；绑定请求 context + 超时：客户端断开或源半挂时不会无限阻塞
// （否则每次查询泄漏一个挂死 goroutine）。
func (h *Handler) fetchTemplateEPG(ctx context.Context, u string) []Program {
	ctx, cancel := context.WithTimeout(ctx, subscriptionFetchTimeout)
	defer cancel()
	resp, err := getFollowRedirects(ctx, h.stream, u)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return nil
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil
	}
	// gzip 魔数识别：模板源常以 .xml.gz 提供整份/单频道 XMLTV，解压后复用同一解析链
	if len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil
		}
		body, err = io.ReadAll(zr)
		zr.Close()
		if err != nil {
			return nil
		}
	}
	return parseEPGContent(body)
}

// parseEPGContent 解析 EPG 模板返回内容，兼容三种形态：
//
//	① JSON 对象 {epg_data:[{start,end,title,...}]}（epg.cdn.loc.cc 等）
//	② XMLTV <programme>
//	③ 纯 JSON 数组 [{title,start,stop}]
func parseEPGContent(body []byte) []Program {
	// ① 对象 {epg_data:[...]}
	var epgObj struct {
		EpgData []struct {
			Start string `json:"start"`
			End   string `json:"end"`
			Title string `json:"title"`
		} `json:"epg_data"`
	}
	if err := json.Unmarshal(body, &epgObj); err == nil && len(epgObj.EpgData) > 0 {
		out := make([]Program, 0, len(epgObj.EpgData))
		for _, e := range epgObj.EpgData {
			out = append(out, Program{Start: e.Start, Stop: e.End, Title: e.Title})
		}
		return out
	}
	// ② XMLTV
	var x struct {
		Programmes []xmltvProgramme `xml:"programme"`
	}
	if err := xml.Unmarshal(body, &x); err == nil && len(x.Programmes) > 0 {
		out := make([]Program, 0, len(x.Programmes))
		for _, p := range x.Programmes {
			title := ""
			if len(p.Title) > 0 {
				title = p.Title[0].Value
			}
			out = append(out, Program{Start: p.Start, Stop: p.Stop, Title: title})
		}
		return out
	}
	// ③ 纯 JSON 数组
	var j []struct {
		Title string `json:"title"`
		Start string `json:"start"`
		Stop  string `json:"stop"`
	}
	if json.Unmarshal(body, &j) == nil && len(j) > 0 {
		out := make([]Program, 0, len(j))
		for _, p := range j {
			out = append(out, Program{Start: p.Start, Stop: p.Stop, Title: p.Title})
		}
		return out
	}
	return nil
}

// ServeCatchup GET /api/player/catchup?key=<频道key>&from=<unix秒>&to=<unix秒>
// 基于 EPG 回看：时间参数用 Unix 秒（无时区歧义），服务端换算成源侧 playseek 所需的
// YmdHis 本地时间串（源侧减8h转UTC），在频道源地址拼 playseek=<start>-<end>，
// 登记短 token 返回 /player/<key>/<token>。
func (h *Handler) ServeCatchup(w http.ResponseWriter, r *http.Request) {
	if !h.requireToken(w, r) {
		return
	}
	key := r.URL.Query().Get("key")
	fromN, errFrom := strconv.ParseInt(r.URL.Query().Get("from"), 10, 64)
	toN, errTo := strconv.ParseInt(r.URL.Query().Get("to"), 10, 64)
	if key == "" || errFrom != nil || errTo != nil || toN <= fromN {
		http.Error(w, "key/from/to required (unix seconds, to > from)", http.StatusBadRequest)
		return
	}
	ch := h.mgr.GetByKey(key)
	if ch == nil {
		http.Error(w, "channel not found", http.StatusForbidden)
		return
	}
	// http(s)/php/rtsp 源支持回看：拼接 playseek 后仍走各自播放链路
	// （php 解析脚本如 xxx 自行处理 playseek；rtsp 由源侧时移服务处理）。
	switch ch.Scheme {
	case "http", "https", "php", "rtsp":
	default:
		http.Error(w, "catchup not supported for this source", http.StatusBadRequest)
		return
	}
	// 回看启动即失效该频道的直播解析缓存（会话切换，返回直播时重新解析）
	h.clearRedirect(key)

	start := time.Unix(fromN, 0).Format("20060102150405")
	end := time.Unix(toN, 0).Format("20060102150405")
	u := catchupURL(ch.RawURL, start, end)
	tok := shortHash(u)
	h.storeResources(key, map[string]string{tok: u})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	writeJSON(w, map[string]interface{}{"play": "/player/" + key + "/" + tok})
}

// catchupURL 在源地址上拼 playseek=<start>-<end>（回看参数，源侧处理时差）。
// 中国移动 OTT 源（路径含 /PLTV/ 段）回看需将 PLTV 替换为 TVOD（时移服务器路径），
// 如 ott.fj.chinamobile.com/PLTV/.../index.m3u8 → ott.fj.chinamobile.com/TVOD/.../index.m3u8。
func catchupURL(raw, start, end string) string {
	raw = strings.Replace(raw, "/PLTV/", "/TVOD/", 1)
	sep := "?"
	if strings.Contains(raw, "?") {
		sep = "&"
	}
	return raw + sep + "playseek=" + start + "-" + end
}

// ServePull GET /player/<key>[/<子路径>] → 受控拉流。
// key 无记录即 403；HLS 子分片仅允许同源相对路径。
func (h *Handler) ServePull(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/player/")
	parts := strings.SplitN(rest, "/", 2)
	key := parts[0]
	sub := ""
	if len(parts) == 2 {
		sub = parts[1]
	}
	ch := h.mgr.GetByKey(key)
	if ch == nil {
		http.Error(w, "channel not found", http.StatusForbidden)
		return
	}
	if !h.requireToken(w, r) {
		return
	}
	defer h.registerClient(r, "player")

	// 子路径：优先按「短 token」查真实上游 URL（m3u8 重写时登记），否则回退同源/来源解析。
	if sub != "" {
		if ch.Scheme != "http" && ch.Scheme != "https" && ch.Scheme != "php" && ch.Scheme != "rtsp" {
			http.Error(w, "sub resource not allowed", http.StatusForbidden)
			return
		}
		if strings.Contains(sub, "://") {
			http.Error(w, "sub resource not allowed", http.StatusForbidden)
			return
		}
		abs := h.resolveToken(key, sub)
		if abs == "" {
			if origin := h.getSegOrigin(key); origin != "" {
				abs = origin + "/" + strings.TrimPrefix(sub, "/")
			} else if a, ok := resolveSub(ch.RawURL, sub); ok {
				abs = a
			}
		}
		if abs == "" {
			http.Error(w, "sub resource not allowed", http.StatusForbidden)
			return
		}
		// 回看签发的 token 可能解析出 php://（脚本含 playseek 参数）或
		// rtsp://（源侧时移）地址，需按协议分派，不能直接进 http 拉流。
		switch {
		case strings.HasPrefix(abs, "php://"):
			h.servePHPRaw(w, r, ch, abs)
			return
		case strings.HasPrefix(abs, "rtsp://"):
			addr := strings.TrimPrefix(abs, "rtsp://")
			r2 := r.Clone(r.Context())
			r2.URL = &url.URL{Path: "/rtsp/" + addr, RawQuery: r.URL.RawQuery}
			handler.RtspToHTTPHandler(w, r2)
			return
		}
		h.serveHTTP(w, r, ch, abs)
		return
	}

	switch ch.Scheme {
	case "udp", "rtp":
		prefix := "/" + ch.Scheme + "/"
		addr := strings.TrimPrefix(ch.RawURL, ch.Scheme+"://")
		if i := strings.Index(addr, "?"); i >= 0 {
			addr = addr[:i]
		}
		r2 := r.Clone(r.Context())
		r2.URL = &url.URL{Path: prefix + addr, RawQuery: r.URL.RawQuery}
		handler.UdpRtpHandler(w, r2, prefix)
	case "rtsp":
		addr := strings.TrimPrefix(ch.RawURL, "rtsp://")
		r2 := r.Clone(r.Context())
		r2.URL = &url.URL{Path: "/rtsp/" + addr, RawQuery: r.URL.RawQuery}
		handler.RtspToHTTPHandler(w, r2)
	case "php":
		h.servePHP(w, r, ch)
	case "http", "https":
		h.serveHTTP(w, r, ch, ch.RawURL)
	default:
		http.Error(w, "unsupported scheme", http.StatusBadRequest)
	}
}

// servePHP 内部执行 php:// 频道源脚本（如 php://php/xxx.php?id=cctv1）：
// 不走 HTTP 回环、无 IP 依赖，直接由内嵌 phpgo 解释器执行并捕获输出。
// 输出处理：
//   - 302/Location（xxx 类解析脚本）→ 以解析出的真实源地址走 http 拉流链路
//     （代理组 + 重定向跟随 + m3u8 分片重写）
//   - 输出体为 m3u8 → 同 http 源：分片重写为受控短地址
//   - 其他输出（TS 连流等）→ 原样透传
func (h *Handler) servePHP(w http.ResponseWriter, r *http.Request, ch *Channel) {
	h.servePHPRaw(w, r, ch, ch.RawURL)
}

// servePHPRaw 执行 php:// 脚本地址（raw 可为频道源地址或回看等场景拼好
// playseek 参数后的地址），输出处理同上。
func (h *Handler) servePHPRaw(w http.ResponseWriter, r *http.Request, ch *Channel, rawURL string) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	raw := strings.TrimPrefix(rawURL, "php://")
	rel := raw
	query := url.Values{}
	if i := strings.Index(raw, "?"); i >= 0 {
		rel = raw[:i]
		if q, err := url.ParseQuery(raw[i+1:]); err == nil {
			query = q
		}
	}
	// 剔除 token 参数，避免进入脚本 $_GET
	if gt := auth.GetGlobalTokenManager(); gt != nil {
		param := gt.TokenParamName
		if param == "" {
			param = "my_token"
		}
		query.Del(param)
	}

	status, hdr, body, err := php.Capture(rel, query)
	if err != nil {
		logger.LogPrintf("[player] php 源执行失败 key=%s src=%s err=%v", ch.Key, rawURL, err)
		http.Error(w, "php source failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	// 脚本输出 Location：解析出的真实源地址 → 按 http 源继续拉流
	if loc := strings.TrimSpace(hdr.Get("Location")); loc != "" {
		if strings.HasPrefix(loc, "http://") || strings.HasPrefix(loc, "https://") {
			logger.LogPrintf("[player] php 源解析成功 key=%s loc=%s", ch.Key, func() string {
				if u, err := url.Parse(loc); err == nil {
					return u.Host
				}
				return "(parse failed)"
			}())
			h.serveHTTP(w, r, ch, loc)
			return
		}
		logger.LogPrintf("[player] php 源 Location 不支持 key=%s loc=%s", ch.Key, loc)
		http.Error(w, "php source redirect unsupported", http.StatusBadGateway)
		return
	}

	// 输出体：合成响应，复用 m3u8 重写/透传管线
	ct := hdr.Get("Content-Type")
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	if ct == "" && len(trimmed) > 0 {
		if bytes.HasPrefix(trimmed, []byte("#EXTM3U")) {
			ct = "application/vnd.apple.mpegurl"
		} else if len(trimmed) >= 188 && trimmed[0] == 0x47 {
			ct = "video/mp2t"
		}
	}
	base := "php://" + rel
	hdr.Set("Content-Type", ct)
	synth := &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     hdr,
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
	if isM3U8(ct, base) {
		rewritten, origin, tokens, werr := rewrittenM3U8(synth.Body, base, ch.Key)
		if werr != nil && len(rewritten) == 0 {
			http.Error(w, "read m3u8 failed", http.StatusBadGateway)
			return
		}
		if origin != "" {
			h.segOrigins.Store(ch.Key, &segOrigin{origin: origin, until: time.Now().Add(segOriginTTL)})
		}
		h.storeResources(ch.Key, tokens)
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(rewritten)
		return
	}
	stream.HandleProxyResponse(ctx, w, r, base, synth, func() {})
}

// serveHTTP 代理拉取远程 http(s) 源到受控端点。
// 服务端跟随重定向（最多 10 次），m3u8 基址用「重定向后的最终 URL」，并把分片重写为短路径，
// 避免 302 Location / 最终源地址回流到浏览器，确保源站地址不外露。
func (h *Handler) serveHTTP(w http.ResponseWriter, r *http.Request, ch *Channel, abs string) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// 频道订阅里的 ua= 优先，否则用 player.ua 默认（缺省内置浏览器 UA）
	ua := ch.UA
	if ua == "" {
		ua = h.mgr.DefaultUA()
	}
	hdr := http.Header{}
	hdr.Set("User-Agent", ua)

	// 302 解析型源（abs 为频道原始地址，如 gdlt.php）：优先用缓存的最终地址，
	// m3u8 刷新不再每次重复执行解析脚本；缓存地址失效时回退重新解析。
	origin := abs
	if abs == ch.RawURL {
		if cu := h.getRedirect(ch.Key); cu != "" {
			abs = cu
		}
	}

	doFetch := func(u string) (*http.Response, error) {
		// 优先走代理组拉流（与 /https:// 原生转发同一机制）：
		//   1) 该频道此前成功用过的代理组（分片 CDN 是 IP/内网地址时规则匹配不上，需沿用同一出口）
		//   2) 否则按域名规则匹配代理组
		// 都未命中或屡次选不到节点（返回 nil resp）→ 直连兜底（h.stream 服务端跟随重定向）。
		resp, usedPg, perr := handler.FetchViaProxyGroup(ctx, u, hdr, true, h.getSegGroup(ch.Key))
		if perr != nil {
			if !errors.Is(perr, context.Canceled) {
				logger.LogPrintf("[player] proxy fetch error key=%s abs=%s err=%v", ch.Key, u, perr)
			}
			return nil, perr
		}
		if resp != nil && usedPg != nil {
			h.storeSegGroup(ch.Key, usedPg)
		}
		if resp == nil {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
			if err != nil {
				return nil, err
			}
			req.Header = hdr.Clone()
			return h.stream.Do(req)
		}
		return resp, nil
	}

	// 上游解析型源（第三方 php 302）不稳定：偶发分流到失效备用链接（返回
	// favicon/HTML 等非媒体内容）或 CDN 风控 403。先判定响应可用性，无效则
	// 重新解析重试（最多 3 次），避免把垃圾内容透传给播放器报"格式不支持"。
	usable := func(resp *http.Response) (bool, int, string, string) {
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return false, resp.StatusCode, "", ""
		}
		// 探测响应头 16 字节判定媒体类型。必须「真实消费」这 16 字节：
		// bufio.Peek 只窥视不消费，若之后仍用同一个 bufio.Reader 回放，
		// 会从缓冲区位置 0 重放（大响应=整个 bufio 缓冲），相当于在响应
		// 开头重复插入 16 字节——m3u8 出现垃圾前缀行，TS 分片起始失步
		// （解复用器每个分段都要重新同步，视频卡顿、音频 PTS 锚点错乱）。
		br := bufio.NewReader(resp.Body)
		head := make([]byte, 16)
		n, _ := io.ReadFull(br, head)
		head = head[:n]
		resp.Body = io.NopCloser(io.MultiReader(bytes.NewReader(head), br))
		trimmed := bytes.TrimLeft(head, " \t\r\n")
		final := ""
		if resp.Request != nil && resp.Request.URL != nil {
			final = resp.Request.URL.String()
		}
		switch {
		case bytes.HasPrefix(trimmed, []byte("#EXT")):
			return true, resp.StatusCode, string(head), final
		case len(head) > 0 && head[0] == 0x47:
			return true, resp.StatusCode, "TS", final
		case len(head) >= 12 && string(head[4:8]) == "ftyp":
			return true, resp.StatusCode, "MP4", final
		case bytes.HasPrefix(head, []byte("FLV")):
			return true, resp.StatusCode, "FLV", final
		default:
			// 兜底：最终 URL 带常见媒体扩展名也视为可用
			p := ""
			if resp.Request != nil && resp.Request.URL != nil {
				p = strings.ToLower(resp.Request.URL.Path)
			}
			ok := strings.HasSuffix(p, ".ts") || strings.HasSuffix(p, ".flv") ||
				strings.HasSuffix(p, ".mp4") || strings.HasSuffix(p, ".m3u8") ||
				strings.HasSuffix(p, ".aac") || strings.HasSuffix(p, ".mp3")
			return ok, resp.StatusCode, string(head), final
		}
	}
	const maxUpstreamAttempts = 3
	// 重试仅对解析型源（origin == ch.RawURL）有意义：会丢弃缓存地址、回到频道原始地址
	// 重跑 302 解析链。非解析型（HLS 分片、解析后的最终地址）重试只是重复请求同一地址：
	// 分片在整点切换时上游要数十秒才产出可用分片，本地重试毫无帮助，且把日志成倍放大——
	// 改由前端「丢批次 + 刷新播放列表取新分片」消化（见 media-engine 分段源恢复）。
	attemptLimit := maxUpstreamAttempts
	if origin != ch.RawURL {
		attemptLimit = 1
	}
	var resp *http.Response
	for attempt := 0; attempt < attemptLimit; attempt++ {
		if attempt > 0 {
			// 无效响应：解析型源丢弃缓存地址、回到频道原始地址重跑 302 解析链。
			// 非解析型（分片等）没有解析缓存可清——redirects 是频道级共用，误清会把
			// 正在使用的直播解析结果一起丢掉（分片 404 重试时尤其明显）。
			if origin == ch.RawURL {
				h.clearRedirect(ch.Key)
			}
			abs = origin
			time.Sleep(time.Duration(attempt) * 200 * time.Millisecond)
		}
		var err error
		resp, err = doFetch(abs)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return // 客户端断开
			}
			logger.LogPrintf("[player] upstream fetch error key=%s abs=%s err=%v", ch.Key, abs, err)
			http.Error(w, "upstream fetch failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		if ok, code, head, final := usable(resp); ok {
			break
		} else {
			logger.LogPrintf("[player] upstream invalid key=%s 状态=%d 内容=%q 最终=%s", ch.Key, code, head, final)
			// 整点跨小时目录纠偏：源站 m3u8 在整点时刻会把「内容属于上一小时」的分片目录
			// 写成当前小时（实测 13:00 的 178962117 在 13 点目录 404、12 点目录 200；
			// 11:59:2x~5x 同理在 11 点目录），于是整点后约 50 秒内的分片请求全部 404。
			// 回退尝试上一小时目录——内容本身没丢，命中即正常返回：前端零感知、时间轴连续。
			if code == http.StatusNotFound {
				if alt, shifted := shiftSegmentHourDir(abs, -1); shifted {
					altResp, altErr := doFetch(alt)
					if altErr == nil {
						if ok2, _, _, _ := usable(altResp); ok2 {
							logger.LogPrintf("[player] 分片跨小时目录回退命中 key=%s alt=%s", ch.Key, alt)
							resp = altResp
							abs = alt
							break
						}
						io.Copy(io.Discard, io.LimitReader(altResp.Body, 4<<20))
						altResp.Body.Close()
					} else if !errors.Is(altErr, context.Canceled) {
						logger.LogPrintf("[player] 分片目录回退失败 key=%s alt=%s err=%v", ch.Key, alt, altErr)
					}
				}
			}
		}
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		resp = nil
	}
	if resp == nil {
		logger.LogPrintf("[player] upstream invalid after %d attempts key=%s origin=%s", attemptLimit, ch.Key, origin)
		http.Error(w, "upstream stream unavailable", http.StatusBadGateway)
		return
	}
	// 成功且最终地址与原始地址不同（发生解析重定向）→ 记住并滚动续期。
	// 仅缓存"从频道原始地址（直播）解析"的结果：回看等场景传入的 abs 是
	// 带 playseek 的会话地址，写入会把回看地址混入直播缓存，导致返回直播
	// 时命中回看缓存。
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && resp.Request != nil && resp.Request.URL != nil {
		if final := resp.Request.URL.String(); final != origin && origin == ch.RawURL {
			h.storeRedirect(ch.Key, final)
		}
	}

	// m3u8 基址：若发生重定向，用最终响应 URL（否则相对分片解析会错、且 Location 会暴露源站）
	base := abs
	if resp.Request != nil && resp.Request.URL != nil {
		base = resp.Request.URL.String()
	}

	// 记录上游响应状态（首次即可），便于区分 500 来自源站还是本地
	if resp.StatusCode != http.StatusOK {
		logger.LogPrintf("[player] upstream status key=%s code=%d from=%s path=%s", ch.Key, resp.StatusCode, base, r.URL.Path)
	}

	ct := resp.Header.Get("Content-Type")
	if isM3U8(ct, base) {
		rewritten, origin, tokens, werr := rewrittenM3U8(resp.Body, base, ch.Key)
		resp.Body.Close()
		if werr != nil && len(rewritten) == 0 {
			http.Error(w, "read m3u8 failed", http.StatusBadGateway)
			return
		}
		// 记住该频道分片所在 CDN origin + 登记短 token->真实URL，供 /player/<key>/<token> 回拉
		if origin != "" {
			h.segOrigins.Store(ch.Key, &segOrigin{origin: origin, until: time.Now().Add(segOriginTTL)})
		}
		h.storeResources(ch.Key, tokens)
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(rewritten)
		return
	}

	stream.HandleProxyResponse(ctx, w, r, base, resp, func() {})
}

// shiftSegmentHourDir 把分片 URL 中的「小时目录」前移/后移 deltaHours（-1 = 上一小时）。
// 只处理形如 .../<10位目录>/<数字序号>.<ts|m4s|mp4|aac|mp3> 的分片 URL，其余返回 false。
// 用途：源站 m3u8 在整点时刻可能把跨小时分片的目录写成当前小时，而文件实际在上一小时
// 目录（内容时间所属小时）里——回退尝试即可拿到本应存在的内容。
func shiftSegmentHourDir(rawURL string, deltaHours int) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	p := u.Path
	slash := strings.LastIndex(p, "/")
	if slash <= 0 {
		return "", false
	}
	file := p[slash+1:]
	slash2 := strings.LastIndex(p[:slash], "/")
	if slash2 < 0 {
		return "", false
	}
	dir := p[slash2+1 : slash]
	if len(dir) != 10 || !isAllDigits(dir) {
		return "", false
	}
	dot := strings.LastIndex(file, ".")
	if dot <= 0 || !isAllDigits(file[:dot]) {
		return "", false
	}
	switch strings.ToLower(file[dot+1:]) {
	case "ts", "m4s", "mp4", "aac", "mp3":
	default:
		return "", false
	}
	t, err := time.ParseInLocation("2006010215", dir, time.Local)
	if err != nil {
		return "", false
	}
	newDir := t.Add(time.Duration(deltaHours) * time.Hour).Format("2006010215")
	u.Path = p[:slash2+1] + newDir + p[slash:]
	return u.String(), true
}

// isAllDigits 判断字符串是否非空且全为数字。
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

const segOriginTTL = 30 * time.Minute
const tokenTTL = 30 * time.Minute
const segGroupTTL = 30 * time.Minute

// 解析结果仅在同一活跃会话内复用：连续轮询间隔（≈ targetDuration）远小于该窗口，
// 而换台/回看/返回直播等场景的间隔必然更久 → 重新请求上游获取播放地址。
const redirectActiveWindow = 45 * time.Second

// getRedirect 返回某频道解析型源的缓存最终地址（未学/会话窗口超时则为空）。
func (h *Handler) getRedirect(key string) string {
	v, ok := h.redirects.Load(key)
	if !ok {
		return ""
	}
	rc := v.(*redirectCache)
	now := time.Now()
	if now.Sub(rc.lastUsed) > redirectActiveWindow {
		h.redirects.Delete(key)
		return ""
	}
	rc.lastUsed = now
	return rc.finalURL
}

// storeRedirect 记住某频道解析型源的最终拉流地址（活跃会话内滚动续期）。
func (h *Handler) storeRedirect(key, finalURL string) {
	h.redirects.Store(key, &redirectCache{finalURL: finalURL, lastUsed: time.Now()})
}

// clearRedirect 清除某频道解析型源的最终地址缓存（失效回退时调用）。
func (h *Handler) clearRedirect(key string) { h.redirects.Delete(key) }

// storeResources 登记某频道的 token->上游URL 映射（带过期，超量时惰性清理）。
// 用 LoadOrStore + 每 key 一把锁，保证并发登记/读取同一频道不产生 map 数据竞争。
func (h *Handler) storeResources(key string, tokens map[string]string) {
	if len(tokens) == 0 {
		return
	}
	now := time.Now()
	newRm := &resMap{m: make(map[string]string, len(tokens)+64), until: now.Add(tokenTTL)}
	actual, _ := h.resources.LoadOrStore(key, newRm)
	rm := actual.(*resMap)
	rm.mu.Lock()
	if now.After(rm.until) || rm.m == nil {
		rm.m = make(map[string]string, len(tokens)+64)
	}
	for k, vv := range tokens {
		rm.m[k] = vv
	}
	rm.until = now.Add(tokenTTL)
	rm.mu.Unlock()
}

// resolveToken 返回某频道 token 对应的真实上游 URL（未登记/过期为空）。
func (h *Handler) resolveToken(key, token string) string {
	v, ok := h.resources.Load(key)
	if !ok {
		return ""
	}
	rm := v.(*resMap)
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if time.Now().After(rm.until) {
		h.resources.Delete(key)
		return ""
	}
	return rm.m[token]
}

// getSegOrigin 返回某频道的分片 CDN origin（未学/过期则为空）。
func (h *Handler) getSegOrigin(key string) string {
	v, ok := h.segOrigins.Load(key)
	if !ok {
		return ""
	}
	so := v.(*segOrigin)
	if time.Now().After(so.until) {
		h.segOrigins.Delete(key)
		return ""
	}
	return so.origin
}

// getSegGroup 返回某频道最近成功使用的代理组（未学/过期则为 nil，重新按域名规则匹配）。
func (h *Handler) getSegGroup(key string) *config.ProxyGroupConfig {
	v, ok := h.segGroups.Load(key)
	if !ok {
		return nil
	}
	sg := v.(*segGroup)
	if time.Now().After(sg.until) {
		h.segGroups.Delete(key)
		return nil
	}
	return sg.pg
}

// storeSegGroup 记住某频道成功使用的代理组（LoadOrStore + 覆盖写，保证并发安全）。
func (h *Handler) storeSegGroup(key string, pg *config.ProxyGroupConfig) {
	if pg == nil {
		return
	}
	h.segGroups.Store(key, &segGroup{pg: pg, until: time.Now().Add(segGroupTTL)})
}

func isM3U8(ct, abs string) bool {
	ct = strings.ToLower(ct)
	if strings.Contains(ct, "mpegurl") || strings.Contains(ct, "m3u8") || strings.Contains(ct, "x-mpegurl") {
		return true
	}
	return strings.HasSuffix(strings.Split(abs, "?")[0], ".m3u8")
}

// rewrittenM3U8 把 m3u8 里的资源（分片/VARIANT m3u8/EXT-X-MAP init）统一重写为
// `绝对路径 /player/<key>/<短token>`，并登记 token->真实上游 URL（含 query 签名）。
// 用绝对路径可避免 hls.js 把 /player/<key> 当文件名而丢 key；浏览器只见短地址，CDN path+长 query 不可见。
// 带内嵌 URI 的标签行（如 EXT-X-MEDIA 音频 rendition、EXT-X-I-FRAME-STREAM-INF、EXT-X-KEY）
// 其 URI 同样解析登记后重写，否则独立的音频轨道会以错误的相对路径回拉。
func rewrittenM3U8(body io.Reader, baseStr, key string) ([]byte, string, map[string]string, error) {
	raw, err := io.ReadAll(io.LimitReader(body, 8<<20))
	if err != nil {
		return nil, "", nil, err
	}
	base, berr := url.Parse(baseStr)
	if berr != nil || base.Host == "" {
		return raw, "", nil, nil
	}
	origin := ""
	tokens := map[string]string{}
	var out bytes.Buffer
	for _, line := range strings.Split(string(raw), "\n") {
		lt := strings.TrimSpace(line)
		if strings.HasPrefix(lt, "#") {
			if strings.Contains(lt, `URI="`) {
				line = m3u8URIRe.ReplaceAllStringFunc(line, func(match string) string {
					sub := m3u8URIRe.FindStringSubmatch(match)[1]
					seg, err := resolveSegment(sub, base)
					if err != nil {
						return match
					}
					abs := seg.String()
					tok := shortHash(abs)
					tokens[tok] = abs
					return `URI="/player/` + key + `/` + tok + `"`
				})
			}
			out.WriteString(line + "\n")
			continue
		}
		if lt == "" {
			out.WriteString(line + "\n")
			continue
		}
		seg, err := resolveSegment(lt, base)
		if err != nil {
			out.WriteString(line + "\n")
			continue
		}
		if origin == "" && refIsAbs(lt) {
			origin = seg.Scheme + "://" + seg.Host
		}
		abs := seg.String()
		tok := shortHash(abs)
		tokens[tok] = abs
		out.WriteString("/player/" + key + "/" + tok + "\n")
	}
	if origin == "" && base.Host != "" {
		origin = base.Scheme + "://" + base.Host
	}
	return out.Bytes(), origin, tokens, nil
}

// shortHash 生成 URL 的短不透明 token（sha1 前 10 位十六进制）。
func shortHash(s string) string {
	h := sha1.Sum([]byte(s))
	return hex.EncodeToString(h[:])[:10]
}

// refIsAbs 判断该分片行是否为绝对 URL（含 scheme://）。
func refIsAbs(lt string) bool {
	return strings.Contains(lt, "://")
}

// resolveSegment 把 m3u8 里的分片行解析为绝对 URL（相对行按 base 解析，绝对行直接用）。
func resolveSegment(lt string, base *url.URL) (*url.URL, error) {
	ref, err := url.Parse(lt)
	if err != nil {
		return nil, err
	}
	if ref.IsAbs() {
		return ref, nil
	}
	return base.ResolveReference(ref), nil
}

// resolveSub 把 m3u8 的相对子路径解析为绝对 URL，并要求与原源同 scheme+host（子路径白名单）。
func resolveSub(baseURL, sub string) (string, bool) {
	base, err := url.Parse(baseURL)
	if err != nil || base.Host == "" {
		return "", false
	}
	// 拒绝 scheme 注入
	if strings.Contains(sub, "://") {
		return "", false
	}
	resolved, err := url.Parse(sub)
	if err != nil {
		return "", false
	}
	abs := base.ResolveReference(resolved)
	if !strings.EqualFold(abs.Scheme, base.Scheme) || abs.Host != base.Host {
		return "", false
	}
	return abs.String(), true
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Write(b)
}
