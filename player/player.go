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
	"reflect"
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

// LineInfo 是组内聚合后的一条线路：拉流仍走各线路自己的 opaque key，
// 切线路 = 换 key 重 tune，白名单/重写/缓存链路不变。
type LineInfo struct {
	Key    string `json:"key"`
	Tag    string `json:"tag,omitempty"`    // 画质/来源标记（从名称尾部提取，仅展示）
	Scheme string `json:"scheme,omitempty"` // 该线路自身的协议，前端逐线路判定回看能力
}

// Channel 解析自订阅的单个频道。
// RawURL 为真实源地址，仅存在于服务端；对外只暴露 Key。
// 组内聚合后（Reload 时），同分组内名称完全一致的频道合并为首条频道的
// 多线路（"北京卫视4K" 与 "北京卫视" 名称不同，是两个频道）：
// ID 为频道稳定 ID（不随线路增减变化），Lines 含全部线路（首项即自身）。
// 单线路频道 Lines 长度为 1。
type Channel struct {
	Key     string      `json:"key"`
	ID      string      `json:"id"` // hash(group|名称)，收藏/续播/线路记忆绑定它
	Name    string      `json:"name"`
	Group   string      `json:"group"`
	Scheme  string      `json:"scheme"` // udp / rtp / rtsp / http / https
	RawURL  string      `json:"-"`      // 真实源，不外露
	UA      string      `json:"-"`      // 每条源的服务端 UA（如需要）
	TVGID   string      `json:"tvgId"`
	TVGName string      `json:"tvgName"`
	TVGLogo string      `json:"tvgLogo"`
	EpgType string      `json:"epgType"` // m3u / txt / none
	Lines   []*LineInfo `json:"lines"`
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
	// epgSource 为主来源（优先级最高那个，界面据此判断"是否配了 EPG"）；
	// epgURLs  为**全部** xml 型来源（订阅内嵌 + 配置），EPGBank 逐个拉取解析后按频道合并；
	// epgTpls  为**全部** template 型来源（订阅内嵌 + 配置），查询时按序请求并合并结果。
	// 两类来源互为补齐：主类型查询无结果时用另一类型补齐（见 Handler.serveEPGQuery）。
	epgSource EPGSource
	epgURLs   []string
	epgTpls   []string
	epg       *EPGBank

	cfg        *config.PlayerConfig
	httpClient *http.Client
	// sources 为最近一次 Reload 实际使用的订阅源列表（多订阅：subscription + subscriptions 合并去重）。
	sources []string
	stop    chan struct{}
	// resetCh 配置热加载通知：收到后立即按新配置重载并重置刷新计时
	// （否则新 update_interval 要等当前周期计时器到期才生效，最长延迟一个周期）。
	resetCh chan struct{}
	// appliedCfg 最近一次**真正应用**的 player 段，供 NotifyPlayerConfigChanged 判定"配置变了没"。
	appliedMu  sync.Mutex
	appliedCfg config.PlayerConfig
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

// NotifyPlayerConfigChanged 配置（重）加载完成后调用，next 为新读入的 player 段：
// 仅当它与"管理器**已应用**的配置"不同才通知重载。可以在所有加载路径上重复调用。
//
// 为什么不交给调用方比较"加载前后"的 config.Cfg.Player：后台保存配置的路径会先把新配置
// load 进内存，随后文件监听再 load 一次 —— 第二次比较时前后都是新值，被判定为"没变化"，
// 通知被静默丢弃，播放器就一直用旧频道表（实测：给配置加上 player.logo 后前台台标永远
// 不出现，重启才好）。改成与"实际生效的配置"对比后，谁先发现变化谁触发，重复调用空转。
func NotifyPlayerConfigChanged(next config.PlayerConfig) {
	if globalHandler == nil {
		return
	}
	m := globalHandler.mgr
	if m == nil {
		return
	}
	m.appliedMu.Lock()
	changed := !reflect.DeepEqual(m.appliedCfg, next)
	m.appliedMu.Unlock()
	if !changed {
		return
	}
	NotifyConfigChanged()
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

// epgSources 把配置里的 EPG 来源展开为有序列表（多 EPG 支持）：
//   - Epg 支持换行/分号分隔；逗号仅当拆分后**每一段都像来源**（http 开头或含 { 占位符）
//     时才当分隔符，避免把 URL 查询串里的逗号误切（如 ?ids=1,2）；
//   - Epgs 列表逐项追加，每项同样做分隔展开；
//   - 逐项 trim、丢空、按原序去重。
func epgSources(p config.PlayerConfig) []string {
	raw := splitEPGValue(p.Epg)
	for _, s := range p.Epgs {
		raw = append(raw, splitEPGValue(s)...)
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

// splitEPGValue 展开单个 EPG 配置项：换行/分号始终是分隔符；逗号按"每段都像来源"判定
// （见 epgSources），否则整串按一个来源处理。
func splitEPGValue(v string) []string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	lines := strings.FieldsFunc(v, func(r rune) bool { return r == '\n' || r == '\r' || r == ';' })
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.Contains(line, ",") {
			out = append(out, line)
			continue
		}
		parts := strings.Split(line, ",")
		splitOK := true
		for _, part := range parts {
			if !looksLikeEPGSource(part) {
				splitOK = false
				break
			}
		}
		if !splitOK {
			out = append(out, line)
			continue
		}
		for _, part := range parts {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

// looksLikeEPGSource 粗判一段文本是否是 EPG 来源：http(s) 地址，或含 { 占位符的模板。
func looksLikeEPGSource(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "http") || strings.Contains(s, "{")
}

// classifyEPGSource 判定单个 EPG 来源类型：含 { 占位符 → template（按频道逐条请求）；
// http(s) 开头且不含 { → xml（整份 XMLTV，gzip 自动识别）；其它写法忽略（Type "none"）。
func classifyEPGSource(u string) EPGSource {
	u = strings.TrimSpace(u)
	switch {
	case u == "":
		return EPGSource{Type: "none"}
	case strings.Contains(u, "{"):
		return EPGSource{Type: "template", URL: u}
	case strings.HasPrefix(u, "http"):
		return EPGSource{Type: "xml", URL: u}
	}
	return EPGSource{Type: "none"}
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
	// EPG 来源组装（多来源 + 合并，详见 doc/PLAYER.md「EPG 节目单」）：
	// 优先级 = 订阅内嵌来源 > 配置 player.epg > player.epgs（各自按书写顺序）。
	//   - xml 型来源全部进 epgURLs：EPGBank 逐个拉取解析后按频道合并节目单，
	//     单个来源失效只是少它那份数据，其余来源照常生效；
	//   - template 型来源全部进 epgTpls：查询时按序请求（填 {name}/{date}）并合并结果。
	// 两类来源互为补齐：主类型查不到该频道节目时用另一类型补齐（跨类型合并）。
	srcList := make([]EPGSource, 0, len(p.Epgs)+2)
	if epgSrc.Type != "none" && epgSrc.URL != "" {
		srcList = append(srcList, epgSrc)
	}
	for _, u := range epgSources(p) {
		if s := classifyEPGSource(u); s.Type != "none" {
			srcList = append(srcList, s)
		}
	}
	var xmlURLs, tplURLs []string
	seenSrc := map[string]bool{}
	for _, s := range srcList {
		key := s.Type + "\x00" + s.URL
		if seenSrc[key] {
			continue
		}
		seenSrc[key] = true
		if s.Type == "xml" {
			xmlURLs = append(xmlURLs, s.URL)
		} else {
			tplURLs = append(tplURLs, s.URL)
		}
	}
	// 主来源：优先级最高的那个（沿用其类型/地址/台标模板）；无来源时 Type 保持 "none"
	mainSrc := EPGSource{Type: "none"}
	if len(srcList) > 0 {
		mainSrc = EPGSource{Type: srcList[0].Type, URL: srcList[0].URL, Logo: epgSrc.Logo}
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
	// 外部台标死链探测：确认 404/410 的置空（限并发 + 总预算 + TTL 缓存）。
	// 必须在组内聚合之前——死链先清掉，聚合头部 TVGLogo 才能落到存活的线路。
	m.pruneDeadLogos(newOrder)
	// 组内聚合：同分组内名称完全一致 → 一个频道多线路（白名单保持全线路）
	newOrder = aggregateIntraGroup(newOrder)
	m.mu.Lock()
	m.channels = newCh
	m.byURL = newByURL
	m.order = newOrder
	m.groups = newGroups
	m.epgSource = mainSrc
	m.epgURLs = xmlURLs
	m.epgTpls = tplURLs
	m.mu.Unlock()

	// 记账：本次 player 段已真正应用（供 NotifyPlayerConfigChanged 判定"变了没"）
	m.appliedMu.Lock()
	m.appliedCfg = p
	m.appliedMu.Unlock()

	if len(sources) > 1 || len(files) > 1 {
		logger.LogPrintf(
			"✅ [player] 订阅加载完成: %d 频道 / %d 分组（%d 个订阅源 / %d 个文件合并）",
			len(newOrder), len(newGroups), len(sources), len(files),
		)
	} else {
		logger.LogPrintf("✅ [player] 订阅加载完成: %d 频道 / %d 分组", len(newOrder), len(newGroups))
	}

	// EPG：全部 xml 型来源由服务端拉取解析（整份 XMLTV，gzip 自动识别）后按频道合并；
	// 单个来源失效只少它那份数据，其余来源照常生效
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
// fetchRedirectMaxHops 订阅 / EPG 这类短拉取最多跟随的重定向跳数。
const fetchRedirectMaxHops = 5

// getFollowRedirects 发起 GET 并**手动跟随 3xx 重定向**（最多 fetchRedirectMaxHops 跳）。
// 本项目的 HTTP 基座 client 不自动跟随重定向（见 utils/http 的 CheckRedirect：3xx 交调用方
// 处理），但这两类拉取必须跟：
//   - 订阅源：http → https 升级（源站把 http 301 到 https）、域名/CDN 搬迁，很常见；
//   - EPG 源：整份 xml.gz 常跳到 CDN 域名（如 epg.51zmt.top:8000/e1.xml.gz → s.xxx.xyz）。
// 相对 Location 按当前地址解析；只接受 http/https（拒绝重定向到 file:// 等其它 scheme）。
func getFollowRedirects(ctx context.Context, client *http.Client, rawURL string) (*http.Response, error) {
	u := rawURL
	for hop := 0; hop <= fetchRedirectMaxHops; hop++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 11) AppleWebKit/537.36")
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 300 || resp.StatusCode >= 400 {
			return resp, nil
		}
		loc := strings.TrimSpace(resp.Header.Get("Location"))
		base := req.URL
		if resp.Request != nil && resp.Request.URL != nil {
			base = resp.Request.URL
		}
		resp.Body.Close()
		if loc == "" {
			return nil, fmt.Errorf("重定向缺少 Location: %s", u)
		}
		next, err := base.Parse(loc)
		if err != nil {
			return nil, err
		}
		if next.Scheme != "http" && next.Scheme != "https" {
			return nil, fmt.Errorf("重定向到不支持的协议 %q: %s", next.Scheme, next.String())
		}
		logger.LogPrintf("↪️ [player] 重定向跟随: %s → %s", u, next.String())
		u = next.String()
	}
	return nil, fmt.Errorf("重定向次数过多: %s", rawURL)
}

// fetch 拉取订阅内容：
//   - http(s)：跟随 3xx 重定向（http → https 升级等），总超时 subscriptionFetchTimeout；
//   - php:// 脚本：由内嵌 phpgo 执行；
//   - 本地路径：file:// / docroot 相对 / 绝对路径直接读文件（64MB 上限）。
func (m *Manager) fetch(src string) []byte {
	if isPHPScriptSource(src) {
		return m.execPHPScriptSource(src)
	}
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		// 订阅拉取是短请求，加总超时兜底（见 subscriptionFetchTimeout 注释）
		ctx, cancel := context.WithTimeout(context.Background(), subscriptionFetchTimeout)
		defer cancel()
		resp, err := getFollowRedirects(ctx, m.httpClient, src)
		if err != nil {
			logger.LogPrintf("❌ [player] 订阅源拉取失败: %v", err)
			return nil
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			logger.LogPrintf("❌ [player] 订阅源拉取失败: HTTP %d (%s)", resp.StatusCode, src)
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

// EPGTemplates 返回全部 template 型来源（订阅内嵌 + 配置，按优先级）；查询时按序请求后合并。
func (m *Manager) EPGTemplates() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]string(nil), m.epgTpls...)
}

func (m *Manager) EPG() *EPGBank {
	return m.epg
}
