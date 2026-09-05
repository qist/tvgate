package player

import (
	"bytes"
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
	chans := h.mgr.Channels()
	if chans == nil {
		chans = []*Channel{}
	}
	writeJSON(w, map[string]interface{}{
		"channels": chans,
		"epg":      h.mgr.EPGSource(),
	})
}

// ServeEPG GET /api/player/epg?ch=<tvg-id>&name=<频道名>&date=YYYYMMDD → 节目单。
// M3U（x-tvg-url XMLTV）：由服务端解析的 EPGBank 查；txt（模板）：服务端填 {name}/{date} 后拉取，规避前端跨域 CORS。
func (h *Handler) ServeEPG(w http.ResponseWriter, r *http.Request) {
	if !h.requireToken(w, r) {
		return
	}
	ch := r.URL.Query().Get("ch")
	name := r.URL.Query().Get("name")
	date := r.URL.Query().Get("date")
	var progs []Program
	es := h.mgr.EPGSource()
	if es.Type == "template" && es.URL != "" && name != "" {
		u := fillEpgURL(es.URL, name, date)
		progs = h.fetchTemplateEPG(u)
	} else {
		// M3U 或固定 XMLTV：按 ch(tvg-id)，缺时按 name（txt 无 tvg-id 用频道名匹配）
		q := ch
		if q == "" {
			q = name
		}
		progs = h.mgr.EPG().Programs(q, date)
	}
	if progs == nil {
		progs = []Program{}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	writeJSON(w, map[string]interface{}{"programs": progs})
}

// fillEpgURL 把 EPG 模板里的 {name}/{date} 占位符填充为实际值（name 用 URL 转义，date 原样）。
func fillEpgURL(tpl, name, date string) string {
	u := strings.ReplaceAll(tpl, "{name}", url.PathEscape(name))
	return strings.ReplaceAll(u, "{date}", date)
}

// fetchTemplateEPG 服务端拉取 txt 模板 EPG（规避 CORS），尽量解析 XMLTV <programme> 或 JSON。
func (h *Handler) fetchTemplateEPG(u string) []Program {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 11) AppleWebKit/537.36 Chrome/91")
	resp, err := h.stream.Do(req)
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

// ServeCatchup GET /api/player/catchup?key=<key>&start=<YmdHis>&end=<YmdHis>
// 基于 EPG 回看：在频道源地址拼 playseek=<start>-<end>（源侧减8h转UTC），登记短 token 返回 /player/<key>/<token>。
func (h *Handler) ServeCatchup(w http.ResponseWriter, r *http.Request) {
	if !h.requireToken(w, r) {
		return
	}
	key := r.URL.Query().Get("key")
	start := r.URL.Query().Get("start")
	end := r.URL.Query().Get("end")
	if key == "" || start == "" || end == "" {
		http.Error(w, "key/start/end required", http.StatusBadRequest)
		return
	}
	ch := h.mgr.GetByKey(key)
	if ch == nil {
		http.Error(w, "channel not found", http.StatusForbidden)
		return
	}
	// http(s)/php/rtsp 源支持回看：拼接 playseek 后仍走各自播放链路
	// （php 解析脚本如 akmg 自行处理 playseek；rtsp 由源侧时移服务处理）。
	switch ch.Scheme {
	case "http", "https", "php", "rtsp":
	default:
		http.Error(w, "catchup not supported for this source", http.StatusBadRequest)
		return
	}
	// 回看启动即失效该频道的直播解析缓存（会话切换，返回直播时重新解析）
	h.clearRedirect(key)

	u := catchupURL(ch.RawURL, start, end)
	tok := shortHash(u)
	h.storeResources(key, map[string]string{tok: u})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	writeJSON(w, map[string]interface{}{"url": "/player/" + key + "/" + tok})
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

// servePHP 内部执行 php:// 频道源脚本（如 php://php/akmg.php?id=cctv1）：
// 不走 HTTP 回环、无 IP 依赖，直接由内嵌 phpgo 解释器执行并捕获输出。
// 输出处理：
//   - 302/Location（akmg 类解析脚本）→ 以解析出的真实源地址走 http 拉流链路
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

	resp, err := doFetch(abs)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return // 客户端断开
		}
		logger.LogPrintf("[player] upstream fetch error key=%s abs=%s err=%v", ch.Key, abs, err)
		http.Error(w, "upstream fetch failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	// 缓存的最终地址已失效（非 2xx）→ 清缓存，回退原始解析地址重试一次
	if abs != origin && (resp.StatusCode < 200 || resp.StatusCode > 299) {
		h.clearRedirect(ch.Key)
		_ = resp.Body.Close()
		abs = origin
		resp, err = doFetch(abs)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return // 客户端断开
			}
			logger.LogPrintf("[player] upstream fetch error key=%s abs=%s err=%v", ch.Key, abs, err)
			http.Error(w, "upstream fetch failed: "+err.Error(), http.StatusBadGateway)
			return
		}
	}
	// 成功且最终地址与原始地址不同（发生解析重定向）→ 记住并滚动续期
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && resp.Request != nil && resp.Request.URL != nil {
		if final := resp.Request.URL.String(); final != origin {
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
