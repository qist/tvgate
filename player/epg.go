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

// EPGBank 解析并缓存一份 XMLTV 节目单，按频道(id/display-name)+日期查询。
// 支持源链（xml 型主源 + 回退源）：Load/周期刷新按序尝试，第一个解析出数据
// 的源生效；主源（如 M3U 内嵌 x-tvg-url）失效时自动用回退源（如配置 player.epg
// 的固定 XMLTV/gz），全部失败则保留上一次成功的数据。
type EPGBank struct {
	mu     sync.RWMutex
	byChan map[string][]Program
	byName map[string]string // display-name -> channel id
	// normName display-name 归一化别名（去分隔符/质量后缀 4k/hd/高清 等），
	// 供订阅频道名与 EPG 频道名存在后缀变体时模糊匹配；冲突的归一化键不建。
	normName map[string]string
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
	// haveData 是否曾成功解析出数据（供主源失效判定：为 false 时查询方可用
	// 回退源接管；为 true 时查询为空仅代表该频道无节目，不回退）。
	haveData bool
}

func NewEPGBank() *EPGBank {
	return &EPGBank{
		byChan:   make(map[string][]Program),
		byName:   make(map[string]string),
		normName: make(map[string]string),
		stop:     make(chan struct{}),
	}
}

// Load 下载（自动识别 gzip 魔数 0x1f 0x8b）并解析 XMLTV 源链：按序尝试，
// 第一个解析出数据（≥1 频道）的源生效；全部失败保留旧数据。
// 去重：进行中跳过；1 分钟内已尝试过也跳过（Reload 每次都会触发 Load）。
func (b *EPGBank) Load(urls ...string) {
	if len(urls) == 0 {
		return
	}
	b.mu.Lock()
	if b.loading || time.Since(b.lastAttempt) < time.Minute {
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
	for _, u := range urls {
		body := fetchEPGBody(client, u)
		if body == nil {
			continue
		}
		if b.parse(body) {
			b.mu.Lock()
			b.haveData = true
			b.mu.Unlock()
			logger.LogPrintf("✅ [player] EPG 解析完成: %d 频道, 源: %s", b.preferCount(), u)
			return
		}
	}
}

// fetchEPGBody 拉取单个 EPG URL 并返回内容（自动识别 gzip 魔数 0x1f 0x8b 解压）。
// 失败返回 nil；XMLTV 是短拉取，加总超时兜底：源半挂时 io.ReadAll 不再无限阻塞，
// loading 标志能按时释放，后续刷新不被永久挡住。
func fetchEPGBody(client *http.Client, rawURL string) []byte {
	ctx, cancel := context.WithTimeout(context.Background(), subscriptionFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 11) AppleWebKit/537.36")
	resp, err := client.Do(req)
	if err != nil {
		logger.LogPrintf("❌ [player] EPG 拉取失败: %v", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
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
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				b.Load(urls...)
			}
		}
	}()
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

// parse 解析 XMLTV 内容；成功解析出 ≥1 个频道返回 true，否则返回 false（保留旧数据）。
func (b *EPGBank) parse(body []byte) bool {
	var tv xmltv
	if err := xml.Unmarshal(body, &tv); err != nil {
		logger.LogPrintf("❌ [player] XMLTV 解析失败: %v", err)
		return false
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
	if len(byChan) == 0 {
		// 空 XMLTV（无节目）：不算一次成功加载，源链继续尝试下一个
		return false
	}
	// 按 start 排序
	for k := range byChan {
		sort.Slice(byChan[k], func(i, j int) bool {
			return byChan[k][i].Start < byChan[k][j].Start
		})
	}
	b.mu.Lock()
	b.byChan = byChan
	b.byName = byName
	b.normName = normName
	b.loaded = true
	b.mu.Unlock()
	return true
}

// HaveData 是否曾成功解析出节目数据（主源失效判定：false = 整份 XMLTV 从未加载
// 成功，查询方可回退到备用来源）。
func (b *EPGBank) HaveData() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.haveData
}

// Programs 返回某频道当天（date 形如 20260901 或 2026-09-01）的节目，可按 channel id 或 display-name 查。
func (b *EPGBank) Programs(chKey, date string) []Program {
	prefix := datePrefix(date)
	b.mu.RLock()
	list := b.byChan[chKey]
	if len(list) == 0 {
		if id := b.byName[chKey]; id != "" {
			list = b.byChan[id]
		}
	}
	if len(list) == 0 {
		// 归一化模糊匹配：订阅频道名与 EPG 频道名的后缀变体（4K/HD/高清 等）
		if id := b.normName[normalizeChannelName(chKey)]; id != "" {
			list = b.byChan[id]
		}
	}
	b.mu.RUnlock()
	if len(list) == 0 {
		return nil
	}
	out := make([]Program, 0, 8)
	for _, p := range list {
		if len(p.Start) >= 8 && p.Start[:8] == prefix {
			out = append(out, p)
		}
	}
	return out
}

func (b *EPGBank) preferCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.byChan)
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
