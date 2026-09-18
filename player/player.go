package player

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/php"
	httpclient "github.com/qist/tvgate/utils/http"
)

// subscriptionFetchTimeout 订阅/EPG 这类短拉取的总超时。
// 全局 HTTP.Timeout 默认 0（不限制，供流式长连接使用）；若源在响应头之后就
// 半挂（body 卡死），io.ReadAll 会无限阻塞——把订阅刷新循环 goroutine 永久
// 卡死（热加载失效、订阅永不再刷新），或让 EPG 的 loading 标志永占导致
// 再无法刷新。这里用独立超时兜底。
const subscriptionFetchTimeout = 30 * time.Second

// Channel 解析自订阅的单个频道。
// RawURL 为真实源地址，仅存在于服务端；对外只暴露 Key。
type Channel struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Group   string `json:"group"`
	Scheme  string `json:"scheme"` // udp / rtp / rtsp / http / https
	RawURL  string `json:"-"`      // 真实源，不外露
	UA      string `json:"-"`      // 每条源的服务端 UA（如需要）
	TVGID   string `json:"tvgId"`
	TVGName string `json:"tvgName"`
	TVGLogo string `json:"tvgLogo"`
	EpgType string `json:"epgType"` // m3u / txt / none
}

// EPGSource 记录订阅携带的 EPG/台标定义（随 /api/player/channels 下发）。
type EPGSource struct {
	Type string `json:"kind"`     // "xml"（M3U x-tvg-url XMLTV）或 "template"（TXT 模板）
	URL  string `json:"template"` // xml 时：XMLTV 地址；template 时：EPG 模板（{name}/{date}）
	Logo string `json:"logo"`     // TXT 台标模板（{name}）
}

// Manager 持有频道表（= 白名单）与不透明 key 映射。
type Manager struct {
	mu       sync.RWMutex
	channels map[string]*Channel // key -> channel
	byURL    map[string]string   // RawURL -> key
	order    []*Channel          // 频道，按订阅行序
	groups   []string

	// EPG 来源（随 /api/player/epg 查询 + /api/player/channels 下发）：
	// epgSource 为当前主来源；epgURLs 为 xml 型源链（主 + 配置固定 XMLTV 回退），
	// 整份 XMLTV 失效时 EPGBank 自动切换到链内下一个可用源。
	epgSource EPGSource
	epgURLs   []string
	// epgBak 跨类型回退来源（主为 template 时配固定 XMLTV，或主为 xml 时配模板）：
	// 主来源失效（template 查询失败 / xml 从未加载成功）时 ServeEPG 用它兜底。
	epgBak EPGSource
	epg    *EPGBank

	cfg        *config.PlayerConfig
	httpClient *http.Client
	// sources 为最近一次 Reload 实际使用的订阅源列表（多订阅：subscription + subscriptions 合并去重）。
	sources []string
	stop    chan struct{}
	// resetCh 配置热加载通知：收到后立即按新配置重载并重置刷新计时
	// （否则新 update_interval 要等当前周期计时器到期才生效，最长延迟一个周期）。
	resetCh chan struct{}
}

func NewManager(cfg *config.PlayerConfig) *Manager {
	return &Manager{
		channels:   make(map[string]*Channel),
		byURL:      make(map[string]string),
		cfg:        cfg,
		httpClient: httpclient.NewHTTPClient(&config.Cfg, nil),
		epg:        NewEPGBank(),
		stop:       make(chan struct{}),
		// 缓冲 1：配置热加载通知在"刷新协程尚未进入 select / 正在重载"时也不丢
		// （无缓冲 + 非阻塞发送会静默丢弃 → 改配置后订阅不重载）。多次变更合并为一次重载。
		resetCh: make(chan struct{}, 1),
	}
}

// 包级单例，避免多端口多次注册时重复创建 manager/后台任务。
var (
	handlerOnce   sync.Once
	globalHandler *Handler
)

// EnsureHandler 返回播放器全局 handler（首次调用时初始化 manager 并启动刷新）。
func EnsureHandler(cfg *config.PlayerConfig) *Handler {
	handlerOnce.Do(func() {
		m := NewManager(cfg)
		m.Start()
		globalHandler = NewHandler(m)
	})
	return globalHandler
}

// Start 初次加载并周期性刷新订阅（间隔每次刷新时重读当前配置）。
func (m *Manager) Start() {
	m.Reload()
	go func() {
		for {
			iv := m.interval()
			if iv <= 0 {
				iv = 2 * time.Hour
			}
			t := time.NewTimer(iv)
			select {
			case <-m.stop:
				t.Stop()
				return
			case <-t.C:
				m.Reload()
			case <-m.resetCh:
				// 配置热加载：立即按新配置重载订阅，随后循环顶部用新间隔重新计时
				t.Stop()
				m.Reload()
			}
		}
	}()
}

// NotifyConfigChanged 配置热加载后通知 player 管理器：立即按新配置重载订阅
// 并重置刷新计时（update_interval / 订阅源等变更即时生效，无需等当前周期到期）。
// 非阻塞：正在处理时跳过（本次变更并入下一次处理）。
func NotifyConfigChanged() {
	if globalHandler == nil {
		return
	}
	m := globalHandler.mgr
	if m == nil {
		return
	}
	select {
	case m.resetCh <- struct{}{}:
	default:
	}
}

func (m *Manager) Stop() {
	close(m.stop)
}

func (m *Manager) Enabled() bool {
	return m.cfg != nil && m.cfg.Enabled && len(subscriptionSources(*m.cfg)) > 0
}

// subscriptionSources 把配置里的订阅源展开为有序列表（多订阅支持）：
//   - Subscription 支持换行/逗号/分号分隔的多个源；若整串本身就是一个存在的本地文件/目录，
//     按单源处理（避免路径里的逗号被误切）；
//   - Subscriptions 列表的每一项同样做分隔展开，按序追加；
//   - 逐项 trim、丢空、按原序去重（同一源只解析一次）。
func subscriptionSources(p config.PlayerConfig) []string {
	raw := splitSourceList(p.Subscription, true)
	for _, s := range p.Subscriptions {
		raw = append(raw, splitSourceList(s, false)...)
	}
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// splitSourceList 展开单个配置项的多个源；singleFirst 为真时先尝试"整串是一个已存在的本地路径"。
func splitSourceList(v string, singleFirst bool) []string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	// 整串本身就是一个源时不要按分隔符切开：存在的本地文件/目录，或 php:// 脚本源
	// （脚本源带 ?query，里面可能有逗号，切开就废了）
	if singleFirst && !strings.ContainsAny(v, "\r\n") && (isExistingLocalPath(v) || isPHPScriptSource(v)) {
		return []string{v}
	}
	return strings.FieldsFunc(v, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == ';' || r == '|'
	})
}

// isExistingLocalPath 判断 src 是否指向存在的本地文件/目录（file:// / php:// / 绝对/相对 docroot）。
func isExistingLocalPath(src string) bool {
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		return false
	}
	path := src
	switch {
	case strings.HasPrefix(path, "file://"):
		path = strings.TrimPrefix(path, "file://")
	case strings.HasPrefix(path, "php://"):
		path = filepath.Join(docroot(), strings.TrimPrefix(path, "php://"))
	default:
		if !filepath.IsAbs(path) {
			path = filepath.Join(docroot(), path)
		}
	}
	if _, err := os.Stat(path); err != nil {
		return false
	}
	return true
}

// DefaultUA 返回当前配置的默认 User-Agent（热重载后重新读取）；未配置时用内置浏览器 UA。
func (m *Manager) DefaultUA() string {
	if ua := readPlayerCfg().UA; ua != "" {
		return ua
	}
	return "Mozilla/5.0 (Linux; Android 11) AppleWebKit/537.36 Chrome/91"
}

// readPlayerCfg 返回当前全局配置的播放器段（加锁读取热重载后的最新值）。
func readPlayerCfg() config.PlayerConfig {
	config.CfgMu.RLock()
	p := config.Cfg.Player
	config.CfgMu.RUnlock()
	return p
}

// interval 返回当前配置的刷新间隔（读取热重载后的 config.Cfg，而非启动时的陈旧指针）。
func (m *Manager) interval() time.Duration {
	config.CfgMu.RLock()
	iv := config.Cfg.Player.UpdateInterval
	config.CfgMu.RUnlock()
	return iv
}

// Reload 拉取并重解析订阅，重建频道表。
// 注意：热重载会整体替换 config.Cfg（config.Cfg = newCfg），故这里每次都从当前配置取值，
// 避免持有指向旧结构的陈旧指针导致后台修改不生效。
func (m *Manager) Reload() {
	// 热重载会整体替换 config.Cfg（config.Cfg = newCfg），故每次读当前全局配置，
	// 避免持有指向旧结构的陈旧指针导致后台修改不生效。
	p := readPlayerCfg()
	sources := subscriptionSources(p)
	if !p.Enabled || len(sources) == 0 {
		return
	}
	// 同步本实例持有的配置副本，供 Enabled/取源/EPG 使用
	copyCfg := p
	m.cfg = &copyCfg
	m.sources = sources

	// 多订阅：逐个源展开为文件列表后合并。单个源失败只跳过自身（其余源照常加载），
	// 全部源都失败才放弃本次刷新（保留上一次的频道表）。
	var files []subFile
	for _, src := range sources {
		fs := m.fetchAll(src)
		if len(fs) == 0 {
			logger.LogPrintf("⚠️ [player] 订阅源拉取失败(已跳过): %s", src)
			continue
		}
		files = append(files, fs...)
	}
	if len(files) == 0 {
		logger.LogPrintf("❌ [player] 全部订阅源拉取失败: %s", strings.Join(sources, ", "))
		return
	}
	// 多文件（目录订阅）：逐文件独立识别格式解析后合并；EPG/台标取第一个非空来源
	var chans []*Channel
	epgSrc := EPGSource{Type: "none"}
	for _, f := range files {
		cs, es := parseSubscription(f.content, f.name)
		chans = append(chans, cs...)
		if epgSrc.Type == "none" && es.Type != "none" {
			epgSrc = es
		}
	}
	// EPG 兜底：所有文件均未内嵌来源时，用配置 `player.epg`（含 { 占位符→template，否则固定 XMLTV→xml）
	if p.Epg != "" && epgSrc.Type == "none" {
		if strings.Contains(p.Epg, "{") {
			epgSrc = EPGSource{Type: "template", URL: p.Epg, Logo: epgSrc.Logo}
		} else if strings.HasPrefix(p.Epg, "http") {
			epgSrc = EPGSource{Type: "xml", URL: p.Epg, Logo: epgSrc.Logo}
		}
	}
	// EPG 回退链：
	//   ① xml 型源链（epgURLs）——内嵌 x-tvg-url / 固定 XMLTV 失效时，自动改用配置
	//      player.epg 的固定 XMLTV（xml/xml.gz）。同 URL 去重，内嵌先行。
	//   ② 跨类型回退（epgBak）——主来源与配置来源类型不同时（如内嵌 template、
	//      配置固定 XMLTV，或反之），ServeEPG 在主来源失效后用它兜底查询。
	var xmlURLs []string
	bak := EPGSource{Type: "none"}
	if epgSrc.Type == "xml" && epgSrc.URL != "" {
		xmlURLs = append(xmlURLs, epgSrc.URL)
	}
	if p.Epg != "" && !strings.Contains(p.Epg, "{") && strings.HasPrefix(p.Epg, "http") {
		// 配置为固定 XMLTV：并入 xml 源链（内嵌已有则跳过重复）
		dup := false
		for _, u := range xmlURLs {
			if u == p.Epg {
				dup = true
				break
			}
		}
		if !dup {
			if epgSrc.Type == "xml" {
				xmlURLs = append(xmlURLs, p.Epg)
			} else {
				// 主来源非 xml（内嵌 template）：配置固定 XMLTV 作为模板失效时的兜底
				bak = EPGSource{Type: "xml", URL: p.Epg}
			}
		}
	} else if p.Epg != "" && strings.Contains(p.Epg, "{") {
		// 配置为模板：主来源为 xml 时作为失效兜底；主来源同为 template 且 URL 不同时同样兜底
		if epgSrc.Type != "template" || epgSrc.URL != p.Epg {
			bak = EPGSource{Type: "template", URL: p.Epg}
		}
	}
	// 台标模板：内容内嵌 `logo=...`（txt）优先，否则用配置 `player.logo`；M3U/txt 的频道 logo 为空时兜底填充
	logoTpl := epgSrc.Logo
	if logoTpl == "" {
		logoTpl = p.Logo
	}
	// 台标填充：本地 logo_dir 优先（<频道名>.png 等），否则用模板 logoTpl；M3U/txt 已有 tvg-logo 则不覆盖
	logoDir := p.LogoDir
	newCh := make(map[string]*Channel, len(chans))
	newByURL := make(map[string]string, len(chans))
	newOrder := make([]*Channel, 0, len(chans))
	newGroups := make([]string, 0, 32)
	seenGroup := map[string]bool{}
	seenIdentity := make(map[string]bool, len(chans))
	for _, c := range chans {
		if _, dup := newByURL[c.RawURL]; dup {
			// 同源去重（同 URL 不同名取其一），保持 key 稳定
			continue
		}
		if c.Group == "" {
			c.Group = "默认"
		}
		c.Key = m.assignStableKey(c, seenIdentity, newByURL)
		if c.TVGLogo == "" {
			if logoDir != "" && c.Name != "" {
				if f := logoFilePath(logoDir, c.Name); f != "" {
					c.TVGLogo = "/player/logo/" + f
				}
			}
		}
		if c.TVGLogo == "" && logoTpl != "" {
			c.TVGLogo = fillTemplate(logoTpl, "name", c.Name)
		}
		newCh[c.Key] = c
		newByURL[c.RawURL] = c.Key
		newOrder = append(newOrder, c)
		if !seenGroup[c.Group] {
			seenGroup[c.Group] = true
			newGroups = append(newGroups, c.Group)
		}
	}
	m.mu.Lock()
	m.channels = newCh
	m.byURL = newByURL
	m.order = newOrder
	m.groups = newGroups
	m.epgSource = epgSrc
	m.epgURLs = xmlURLs
	m.epgBak = bak
	m.mu.Unlock()

	if len(sources) > 1 || len(files) > 1 {
		logger.LogPrintf(
			"✅ [player] 订阅加载完成: %d 频道 / %d 分组（%d 个订阅源 / %d 个文件合并）",
			len(newOrder), len(newGroups), len(sources), len(files),
		)
	} else {
		logger.LogPrintf("✅ [player] 订阅加载完成: %d 频道 / %d 分组", len(newOrder), len(newGroups))
	}

	// EPG：xml 型源链由服务端拉取解析（整份 XMLTV，gzip 自动识别）；主源失效自动切链内回退源
	if len(xmlURLs) > 0 {
		go m.epg.Load(xmlURLs...)
		m.epg.startRefresh(m.cfg.UpdateInterval, xmlURLs...)
	}
}

// subFile 订阅源解析出的单个文件（name 用于日志与按文件解析）。
type subFile struct {
	name    string
	content []byte
}

// fetchAll 把订阅源展开为文件列表：
//   - http(s) URL / 单文件：返回单个元素；
//   - 目录（支持绝对路径 / file://dir / php://dir）：递归收集其中 .txt/.m3u/.m3u8，
//     跳过隐藏文件/目录，按路径名排序保证合并顺序稳定。
func (m *Manager) fetchAll(src string) []subFile {
	// php:// 脚本源（php://xxx.php?query）：执行脚本，输出即订阅内容（单文件）
	if isPHPScriptSource(src) {
		b := m.fetch(src)
		if b == nil {
			return nil
		}
		return []subFile{{name: src, content: b}}
	}
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		b := m.fetch(src)
		if b == nil {
			return nil
		}
		return []subFile{{name: src, content: b}}
	}
	path := src
	switch {
	case strings.HasPrefix(path, "file://"):
		path = strings.TrimPrefix(path, "file://")
	case strings.HasPrefix(path, "php://"):
		path = filepath.Join(docroot(), strings.TrimPrefix(path, "php://"))
	default:
		if !filepath.IsAbs(path) {
			path = filepath.Join(docroot(), path)
		}
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if !st.IsDir() {
		b := m.fetch(src) // 单文件复用原逻辑（含 file://、php:// 解析）
		if b == nil {
			return nil
		}
		return []subFile{{name: path, content: b}}
	}
	// 目录：递归收集订阅文件
	var out []subFile
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 单个不可读项跳过，不影响其余文件
		}
		if d.IsDir() {
			if p != path && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir // 隐藏目录整体跳过
			}
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") {
			return nil
		}
		switch strings.ToLower(filepath.Ext(name)) {
		case ".txt", ".m3u", ".m3u8":
		default:
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil || len(b) == 0 {
			return nil
		}
		if len(b) > 64<<20 {
			logger.LogPrintf("⚠️ [player] 订阅文件过大跳过(>64MB): %s", p)
			return nil
		}
		out = append(out, subFile{name: p, content: b})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// isPHPScriptSource 判断订阅源是否为 php:// **脚本源**（php://xxx.php?query）。
// 与频道源 php://（handler.servePHPRaw）同一约定：脚本由内嵌 phpgo 解释器执行，输出即订阅内容；
// 其余 php:// 路径（如 php://tv.txt、php://tv/）仍按 docroot 下的静态文件/目录处理。
func isPHPScriptSource(src string) bool {
	rest := strings.TrimPrefix(src, "php://")
	if rest == src {
		return false
	}
	if i := strings.Index(rest, "?"); i >= 0 {
		rest = rest[:i]
	}
	return strings.HasSuffix(strings.ToLower(strings.Trim(rest, "/")), ".php")
}

// execPHPScriptSource 执行 php:// 订阅脚本并返回其输出体：
//   - 脚本输出 Location（http/https）时跟随一次（与频道源解析语义一致）；
//   - 执行失败 / 无输出返回 nil，由调用方按"该源拉取失败"处理（不影响其它源）。
func (m *Manager) execPHPScriptSource(src string) []byte {
	raw := strings.TrimPrefix(src, "php://")
	rel, queryStr := raw, ""
	if i := strings.Index(raw, "?"); i >= 0 {
		rel, queryStr = raw[:i], raw[i+1:]
	}
	query := url.Values{}
	if queryStr != "" {
		if q, err := url.ParseQuery(queryStr); err == nil {
			query = q
		}
	}
	status, hdr, body, err := php.Capture(rel, query)
	if err != nil {
		logger.LogPrintf("⚠️ [player] php 订阅源执行失败: %s (%v)", src, err)
		return nil
	}
	if loc := strings.TrimSpace(hdr.Get("Location")); loc != "" {
		if strings.HasPrefix(loc, "http://") || strings.HasPrefix(loc, "https://") {
			return m.fetch(loc)
		}
		logger.LogPrintf("⚠️ [player] php 订阅源 Location 不支持: %s (%s)", src, loc)
		return nil
	}
	if status != http.StatusOK && len(body) == 0 {
		logger.LogPrintf("⚠️ [player] php 订阅源返回 %d 且无输出: %s", status, src)
		return nil
	}
	return body
}

// fetch 支持 php:// 脚本源、本地文件路径与 http(s) URL。
func (m *Manager) fetch(src string) []byte {
	if isPHPScriptSource(src) {
		return m.execPHPScriptSource(src)
	}
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		// 订阅拉取是短请求，加总超时兜底（见 subscriptionFetchTimeout 注释）
		ctx, cancel := context.WithTimeout(context.Background(), subscriptionFetchTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
		if err != nil {
			return nil
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 11) AppleWebKit/537.36")
		resp, err := m.httpClient.Do(req)
		if err != nil {
			return nil
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20)) // 64MB 上限
		if err != nil {
			return nil
		}
		return b
	}
	// 本地路径：支持 file://、php://（docroot 相对）、相对路径（相对 docroot）、绝对路径
	path := src
	switch {
	case strings.HasPrefix(path, "file://"):
		path = strings.TrimPrefix(path, "file://")
	case strings.HasPrefix(path, "php://"):
		path = filepath.Join(docroot(), strings.TrimPrefix(path, "php://"))
	default:
		if !filepath.IsAbs(path) {
			path = filepath.Join(docroot(), path)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return b
}

// docroot 返回 PHP docroot 目录（用作相对订阅路径基准）；未配置返回当前目录。
func docroot() string {
	if dr := config.Cfg.PHP.DocRoot; dr != "" {
		return dr
	}
	return "."
}

// fillTemplate 把模板里的 {key} 占位符替换为值（URL 安全编码）。
func fillTemplate(tpl, key, value string) string {
	return strings.ReplaceAll(tpl, "{"+key+"}", url.PathEscape(value))
}

// logoFilePath 在 logoDir 下查找 <频道名>.<图片扩展>；找到返回 URL 转义的文件名，否则空。
func logoFilePath(dir, name string) string {
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".webp", ".gif"} {
		base := name + ext
		if _, err := os.Stat(filepath.Join(dir, base)); err == nil {
			return url.PathEscape(base)
		}
	}
	return ""
}

// assignStableKey 生成跨订阅刷新/编辑稳定的频道 key（md5 前 8 + sha1 前 4 = 12 hex）。
//
// 早期按 URL 派生，但订阅源 URL 常带轮换 token 或被订阅方编辑（换源、改参数），
// URL 文本一变 key 就全量轮换：分享的 /pp#key 深链失效，正在播放的频道中途
// "消失"（/player/<key>/... 全部 404），表现为播放卡死。频道身份以「名称+分组」
// 为准（用户认知的台号）；匿名频道或同名同组的第 2 路及以后的多源条目才退回
// 按 URL 派生（URL 变化只影响该路源自身的 key）。
func (m *Manager) assignStableKey(c *Channel, seenIdentity map[string]bool, used map[string]string) string {
	if c.Name != "" {
		identity := c.Name + "\x00" + c.Group
		if !seenIdentity[identity] {
			seenIdentity[identity] = true
			return uniqueKey(hashKey(identity), used)
		}
	}
	return uniqueKey(hashKey(c.RawURL), used)
}

// hashKey 生成 12 hex 短哈希（md5 前 8 + sha1 前 4）。
func hashKey(s string) string {
	h1 := md5.Sum([]byte(s))
	h2 := sha1.Sum([]byte(s))
	return hex.EncodeToString(h1[:])[:8] + hex.EncodeToString(h2[:])[:4]
}

// uniqueKey 冲突时追加递增后缀，保证 used 集合内唯一。
func uniqueKey(base string, used map[string]string) string {
	i := 0
	for {
		k := base
		if i > 0 {
			k = fmt.Sprintf("%s%d", base, i)
		}
		if _, exists := used[k]; !exists {
			return k
		}
		i++
	}
}

func (m *Manager) GetByKey(key string) *Channel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.channels[key]
}

func (m *Manager) Channels() []*Channel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Channel, len(m.order))
	copy(out, m.order)
	return out
}

func (m *Manager) Groups() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.groups
}

func (m *Manager) EPGSource() EPGSource {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.epgSource
}

// EPGFallback 返回跨类型回退来源（主来源失效时 ServeEPG 用它兜底；无则为 Type "none"）。
func (m *Manager) EPGFallback() EPGSource {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.epgBak
}

func (m *Manager) EPG() *EPGBank {
	return m.epg
}
