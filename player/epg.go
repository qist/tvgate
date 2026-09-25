package player

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	httpclient "github.com/qist/tvgate/utils/http"
)

// Program 单条节目（from/to 为 XMLTV 原样时间串，如 20260901080000 +0800）。
type Program struct {
	Start string `json:"from"`
	Stop  string `json:"to"`
	Title string `json:"title"`
}

type xmltvChannel struct {
	ID   string `xml:"id,attr"`
	Name string `xml:"display-name"`
}

type xmltvTitle struct {
	Lang  string `xml:"lang,attr"`
	Value string `xml:",chardata"`
}

type xmltvProgramme struct {
	Channel string       `xml:"channel,attr"`
	Start   string       `xml:"start,attr"`
	Stop    string       `xml:"stop,attr"`
	Title   []xmltvTitle `xml:"title"`
}

type xmltv struct {
	Channels   []xmltvChannel   `xml:"channel"`
	Programmes []xmltvProgramme `xml:"programme"`
}

// EPGBank 解析并缓存 XMLTV 节目单（支持多来源），按频道(id/display-name)+日期查询。
// 多来源在**查询时合并**：每个来源各自按同一套规则解析频道（channel id → display-name
// → 归一化名），命中的节目按开始时间并起来、同一 start 取靠前来源。这样即使两个 EPG
// 服务给同一频道用了不同的 channel id（如 51zmt 用数字 id、别家用自己的 id），只要
// display-name 一致就能对上，节目单照样互补。
type EPGBank struct {
	mu sync.RWMutex
	// sets 已加载来源的数据，顺序即优先级（订阅内嵌来源 → 配置来源）
	sets     []*epgDataset
	loaded   bool
	interval time.Duration
	stop     chan struct{}
	// 刷新循环状态：Reload 会反复调用 startRefresh，必须幂等，
	// 否则每次 Reload 泄漏一个「周期拉取+解析 XMLTV」的 goroutine
	// （update_interval 越短泄漏越快，最终并发解析吃满 CPU）。
	refreshURLs []string
	refreshLive bool
	// Load 去重：进行中标志 + 上次尝试时间（Reload 每次都 go Load，
	// 节流 1 分钟避免 update_interval 很小时反复全量下载+解析 XMLTV）。
	loading     bool
	lastAttempt time.Time
	// haveData 是否曾成功解析出数据（全部来源都失败时保留旧数据，据此可区分
	// "从未加载成功" 与 "该频道确实没有节目"）。
	haveData bool
}

func NewEPGBank() *EPGBank {
	return &EPGBank{stop: make(chan struct{})}
}

// Load 下载（自动识别 gzip 魔数 0x1f 0x8b）并解析全部 xml 型来源，按优先级保存多份数据
// （查询时合并，见 Programs）。逐个来源拉取解析，失败或空内容的只跳过自身，其余来源照常
// 生效；全部来源都拿不到数据则保留旧数据。去重：进行中跳过；1 分钟内已尝试过也跳过
// （Reload 每次都会触发 Load）。
func (b *EPGBank) Load(urls ...string) {
	b.load(false, urls...)
}

// ForceLoad 强制重新下载并解析全部来源：绕过 1 分钟节流（仍保留 loading 去重防并发）。
// 用于 EPG 缓存的每日 0 点定时过期——节目单按天组织，跨天后旧数据不再准确，必须拉新。
func (b *EPGBank) ForceLoad(urls ...string) {
	b.load(true, urls...)
}

func (b *EPGBank) load(force bool, urls ...string) {
	if len(urls) == 0 {
		return
	}
	b.mu.Lock()
	if b.loading || (!force && time.Since(b.lastAttempt) < time.Minute) {
		b.mu.Unlock()
		return
	}
	b.loading = true
	b.lastAttempt = time.Now()
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.loading = false
		b.mu.Unlock()
	}()

	client := httpclient.NewHTTPClient(&config.Cfg, nil)
	sets := make([]*epgDataset, 0, len(urls))
	used := make([]string, 0, len(urls))
	for _, u := range urls {
		body := fetchEPGBody(client, u)
		if body == nil {
			continue
		}
		ds, ok := parseEPGDataset(body)
		if !ok {
			// 空 XMLTV（无节目）：不算一次成功加载，其它来源照常生效
			logger.LogPrintf("⚠️ [player] EPG 来源无可用节目(已跳过): %s", u)
			continue
		}
		sets = append(sets, ds)
		used = append(used, u)
	}
	if len(sets) == 0 {
		return
	}
	b.install(sets)
	logger.LogPrintf("✅ [player] EPG 解析完成: %d 频道(%d 份来源合并), 源: %s",
		b.preferCount(), len(sets), strings.Join(used, ", "))
}

// fetchEPGBody 拉取单个 EPG URL 并返回内容（自动识别 gzip 魔数 0x1f 0x8b 解压）。
// 失败返回 nil；XMLTV 是短拉取，加总超时兜底：源半挂时 io.ReadAll 不再无限阻塞，
// loading 标志能按时释放，后续刷新不被永久挡住。
func fetchEPGBody(client *http.Client, rawURL string) []byte {
	ctx, cancel := context.WithTimeout(context.Background(), subscriptionFetchTimeout)
	defer cancel()
	resp, err := getFollowRedirects(ctx, client, rawURL)
	if err != nil {
		logger.LogPrintf("❌ [player] EPG 拉取失败: %v", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		logger.LogPrintf("❌ [player] EPG 拉取失败: HTTP %d (%s)", resp.StatusCode, rawURL)
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil
	}
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
	return body
}

func (b *EPGBank) startRefresh(interval time.Duration, urls ...string) {
	if len(urls) == 0 {
		return
	}
	if interval <= 0 {
		interval = 2 * time.Hour
	}
	b.mu.Lock()
	// 幂等：同 URL 链同间隔且循环已在跑 → 直接返回（Reload 每次都会调到这里）
	if b.refreshLive && b.interval == interval && sameStrings(b.refreshURLs, urls) {
		b.mu.Unlock()
		return
	}
	// URL 链或间隔变化：停掉旧刷新循环，重新起一个
	if b.stop != nil {
		close(b.stop)
	}
	b.stop = make(chan struct{})
	b.refreshURLs = append([]string(nil), urls...)
	b.interval = interval
	b.refreshLive = true
	stop := b.stop
	b.mu.Unlock()

	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		// EPG 缓存每日本地 0 点强制过期重拉：节目单按天组织，跨天后旧缓存
		// 不再准确；0 点触发走 ForceLoad 绕过节流（周期刷新仍受节流保护）。
		midnight := time.NewTimer(timeUntilMidnight())
		defer midnight.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				b.Load(urls...)
			case <-midnight.C:
				midnight.Reset(timeUntilMidnight())
				b.ForceLoad(urls...)
			}
		}
	}()
}

// timeUntilMidnight 距下一个本地 0 点的时长。
func timeUntilMidnight() time.Duration {
	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Add(24 * time.Hour)
	return next.Sub(now)
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// epgDataset 单个 XMLTV 来源解析出的数据（构造后只读）；多来源查询时逐个解析后合并。
type epgDataset struct {
	byChan   map[string][]Program // channel id -> 节目
	byName   map[string]string    // display-name / channel id -> channel id
	normName map[string]string    // 归一化频道名 -> channel id
}

// parseEPGDataset 解析一份 XMLTV 内容；成功解析出 ≥1 个频道返回数据与 true，
// 解析失败或空内容（无节目）返回 false（调用方保留/跳过，不覆盖已有数据）。
func parseEPGDataset(body []byte) (*epgDataset, bool) {
	var tv xmltv
	if err := xml.Unmarshal(body, &tv); err != nil {
		logger.LogPrintf("❌ [player] XMLTV 解析失败: %v", err)
		return nil, false
	}
	byChan := make(map[string][]Program, len(tv.Programmes))
	for _, p := range tv.Programmes {
		title := ""
		if len(p.Title) > 0 {
			title = p.Title[0].Value
		}
		byChan[p.Channel] = append(byChan[p.Channel], Program{
			Start: p.Start,
			Stop:  p.Stop,
			Title: title,
		})
	}
	if len(byChan) == 0 {
		return nil, false
	}
	// 频道 display-name -> id 别名，便于按频道名查询（txt 订阅无 tvg-id）
	byName := make(map[string]string, len(tv.Channels))
	normName := make(map[string]string)
	for _, c := range tv.Channels {
		name := strings.TrimSpace(c.Name)
		if name != "" {
			byName[name] = c.ID
			byName[c.ID] = c.ID
		}
		// 归一化别名（去分隔符/质量后缀 4k/hd/高清 等）。订阅频道名与 EPG
		// 频道名存在后缀变体（如 "北京卫视4K" vs "北京卫视"）时按此匹配。
		// 多个 display-name 归一化相同（如 "CCTV4K" 与 "CCTV4"）→ 不建别名，
		// 宁缺毋滥，避免错误匹配到无关频道。
		if n := normalizeChannelName(name); n != "" {
			if prev, dup := normName[n]; dup && prev != c.ID {
				delete(normName, n)
			} else {
				normName[n] = c.ID
			}
		}
	}
	return &epgDataset{byChan: byChan, byName: byName, normName: normName}, true
}

// lookup 在**本来源内**按 channel id / display-name / 归一化名解析某频道的节目
// （与原单来源语义一致：channel id 精确 → display-name/id 别名 → 归一化模糊匹配）。
// ds 构造后只读，无需加锁。
func (ds *epgDataset) lookup(chKey string) []Program {
	if len(chKey) == 0 {
		return nil
	}
	if list := ds.byChan[chKey]; len(list) > 0 {
		return list
	}
	if id := ds.byName[chKey]; id != "" {
		if list := ds.byChan[id]; len(list) > 0 {
			return list
		}
	}
	if id := ds.normName[normalizeChannelName(chKey)]; id != "" {
		return ds.byChan[id]
	}
	return nil
}

// install 安装一批来源数据（顺序即优先级）；调用方保证非空。
func (b *EPGBank) install(sets []*epgDataset) {
	b.mu.Lock()
	b.sets = sets
	b.loaded = true
	b.haveData = true
	b.mu.Unlock()
}

// HaveData 是否曾成功解析出节目数据（主源失效判定：false = 整份 XMLTV 从未加载
// 成功，查询方可回退到备用来源）。
func (b *EPGBank) HaveData() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.haveData
}

// Programs 返回某频道当天（date 形如 20260901 或 2026-09-01）的节目，可按 channel id 或
// display-name 查。多来源按优先级合并：逐个来源解析并收集该频道节目，同一 start 只保留
// 靠前来源那条（避免两个来源的同档节目在界面上重影），最后按 start 排序。
func (b *EPGBank) Programs(chKey, date string) []Program {
	prefix := datePrefix(date)
	b.mu.RLock()
	sets := append([]*epgDataset(nil), b.sets...)
	b.mu.RUnlock()
	if len(sets) == 0 || chKey == "" {
		return nil
	}
	var out []Program
	var seen map[string]bool
	for _, ds := range sets {
		for _, p := range ds.lookup(chKey) {
			if len(p.Start) < 8 || p.Start[:8] != prefix {
				continue
			}
			if seen == nil {
				seen = make(map[string]bool, 8)
			}
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

// preferCount 已加载来源的频道数合计（仅日志用：多来源各有自己的 channel id，无法精确去重）。
func (b *EPGBank) preferCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	n := 0
	for _, ds := range b.sets {
		n += len(ds.byChan)
	}
	return n
}

// datePrefix 把日期统一成 YYYYMMDD 前缀。
func datePrefix(date string) string {
	if len(date) == 10 && date[4] == '-' {
		return date[:4] + date[5:7] + date[8:10]
	}
	if len(date) >= 8 {
		return date[:8]
	}
	return ""
}

// epgQualitySuffixes 频道名尾部的画质后缀（不是频道身份，EPG 查询前可剥）。
var epgQualitySuffixes = []string{"4k", "uhd", "fhd", "hd", "高清", "超清", "标清"}

// stripQualitySuffix 剥掉频道名尾部的画质后缀，其余保持原样（大小写/分隔符不动，
// 供模板 EPG 按名查询用：外源服务做的是可读名匹配）。仅当后缀前是分隔符
// （-/_/空格/·）或非 ASCII 字符（中文台名）才剥："CCTV1-4K"→"CCTV1"、
// "北京卫视4K"→"北京卫视"；紧跟数字/字母的不剥——"CCTV4K" 剥成 "CCTV4"、
// "SEPD4K" 剥成 "SEPD" 会变成另一个台名。
func stripQualitySuffix(name string) string {
	for {
		lower := strings.ToLower(name)
		suf := ""
		for _, s := range epgQualitySuffixes {
			if strings.HasSuffix(lower, s) {
				suf = s
				break
			}
		}
		if suf == "" {
			return name
		}
		idx := len(name) - len(suf)
		if idx <= 0 {
			return name
		}
		switch prev := name[idx-1]; {
		case prev == '-' || prev == '_' || prev == ' ' || prev == '·':
			name = name[:idx-1] // 分隔符连同后缀一起剥
		case prev >= 0x80:
			name = name[:idx] // 中文等非 ASCII：只剥后缀
		default:
			return name // 紧跟数字/字母：后缀是名字本体，不剥
		}
	}
}

// normalizeChannelName 归一化频道名用于模糊匹配：小写、去常见分隔符
// （空格/-/_/./·），并剥掉质量后缀变体（4k/hd/高清/超清/fhd/uhd/标清，
// 可叠加）。"北京卫视4K" → "北京卫视"、"CCTV-1 高清" → "cctv1"。
func normalizeChannelName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.NewReplacer("-", "", "_", "", " ", "", ".", "", "·", "").Replace(n)
	for {
		orig := n
		for _, suf := range []string{"4k", "uhd", "fhd", "hd", "高清", "超清", "标清"} {
			if strings.HasSuffix(n, suf) {
				n = strings.TrimSuffix(n, suf)
				break
			}
		}
		if n == orig {
			return n
		}
	}
}
