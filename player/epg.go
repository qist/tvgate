package player

import (
	"bytes"
	"compress/gzip"
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

// Program 单条节目（start/stop 为 XMLTV 原样时间串，如 20260901080000 +0800）。
type Program struct {
	Start string `json:"start"`
	Stop  string `json:"stop"`
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
type EPGBank struct {
	mu       sync.RWMutex
	byChan   map[string][]Program
	byName   map[string]string // display-name -> channel id
	loaded   bool
	interval time.Duration
	stop     chan struct{}
}

func NewEPGBank() *EPGBank {
	return &EPGBank{
		byChan: make(map[string][]Program),
		byName: make(map[string]string),
		stop:   make(chan struct{}),
	}
}

// Load 下载（自动识别 gzip 魔数 0x1f 0x8b）并解析 XMLTV。
func (b *EPGBank) Load(rawURL string) {
	client := httpclient.NewHTTPClient(&config.Cfg, nil)
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 11) AppleWebKit/537.36")
	resp, err := client.Do(req)
	if err != nil {
		logger.LogPrintf("❌ [player] EPG 拉取失败: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return
	}
	// gzip 魔数识别
	if len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return
		}
		body, err = io.ReadAll(zr)
		zr.Close()
		if err != nil {
			return
		}
	}
	b.parse(body)
	logger.LogPrintf("✅ [player] EPG 解析完成: %d 频道", b.preferCount())
}

func (b *EPGBank) startRefresh(rawURL string, interval time.Duration) {
	if interval <= 0 {
		interval = 2 * time.Hour
	}
	b.interval = interval
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-b.stop:
				return
			case <-t.C:
				b.Load(rawURL)
			}
		}
	}()
}

func (b *EPGBank) parse(body []byte) {
	var tv xmltv
	if err := xml.Unmarshal(body, &tv); err != nil {
		logger.LogPrintf("❌ [player] XMLTV 解析失败: %v", err)
		return
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
	for _, c := range tv.Channels {
		name := strings.TrimSpace(c.Name)
		if name != "" {
			byName[name] = c.ID
			byName[c.ID] = c.ID
		}
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
	b.loaded = true
	b.mu.Unlock()
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
