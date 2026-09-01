package player

import (
	"bytes"
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	httpclient "github.com/qist/tvgate/utils/http"
)

// Channel 解析自订阅的单个频道。
// RawURL 为真实源地址，仅存在于服务端；对外只暴露 Key。
type Channel struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Group   string `json:"group"`
	Scheme  string `json:"scheme"` // udp / rtp / rtsp / http / https
	RawURL  string `json:"-"`      // 真实源，不外露
	UA      string `json:"-"`      // 每条源的服务端 UA（如需要）
	TVGID   string `json:"tvg_id"`
	TVGName string `json:"tvg_name"`
	TVGLogo string `json:"tvg_logo"`
	EpgType string `json:"epg_type"` // m3u / txt / none
}

// EPGSource 记录订阅携带的 EPG/台标定义（随 /api/player/channels 下发）。
type EPGSource struct {
	Type string `json:"type"`     // "xml"（M3U x-tvg-url XMLTV）或 "template"（TXT 模板）
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

	epgSource EPGSource
	epg       *EPGBank

	cfg          *config.PlayerConfig
	httpClient   *http.Client
	subscription string
	stop         chan struct{}
}

func NewManager(cfg *config.PlayerConfig) *Manager {
	return &Manager{
		channels:   make(map[string]*Channel),
		byURL:      make(map[string]string),
		cfg:        cfg,
		httpClient: httpclient.NewHTTPClient(&config.Cfg, nil),
		epg:        NewEPGBank(),
		stop:       make(chan struct{}),
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
			}
		}
	}()
}

func (m *Manager) Stop() {
	close(m.stop)
}

func (m *Manager) Enabled() bool {
	return m.cfg != nil && m.cfg.Enabled && m.cfg.Subscription != ""
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
	if !p.Enabled || p.Subscription == "" {
		return
	}
	// 同步本实例持有的配置副本，供 Enabled/取源/EPG 使用
	copyCfg := p
	m.cfg = &copyCfg

	src := copyCfg.Subscription
	content := m.fetch(src)
	if content == nil {
		logger.LogPrintf("❌ [player] 订阅拉取失败: %s", src)
		return
	}
	chans, epgSrc := parseSubscription(content, src)
	// txt 订阅的 EPG：内容内嵌 `epg=...` 优先；否则用配置 `player.epg`（含 { 占位符→template，否则固定 XMLTV→xml）
	if !bytes.HasPrefix(bytes.TrimLeft(content, "\xef\xbb\xbf \t\r\n"), []byte("#EXTM3U")) {
		if p.Epg != "" && epgSrc.Type == "none" {
			if strings.Contains(p.Epg, "{") {
				epgSrc = EPGSource{Type: "template", URL: p.Epg, Logo: epgSrc.Logo}
			} else if strings.HasPrefix(p.Epg, "http") {
				epgSrc = EPGSource{Type: "xml", URL: p.Epg, Logo: epgSrc.Logo}
			}
		}
	}
	// 非 M3U 时也可用配置提供固定 XMLTV（pp.xml 等），覆盖无 x-tvg-url 的场景
	if bytes.HasPrefix(bytes.TrimLeft(content, "\xef\xbb\xbf \t\r\n"), []byte("#EXTM3U")) && p.Epg != "" && epgSrc.Type == "none" && !strings.Contains(p.Epg, "{") && strings.HasPrefix(p.Epg, "http") {
		epgSrc = EPGSource{Type: "xml", URL: p.Epg}
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
	for _, c := range chans {
		if _, dup := newByURL[c.RawURL]; dup {
			// 同源去重（同 URL 不同名取其一），保持 key 稳定
			continue
		}
		c.Key = m.assignKey(c.RawURL, newByURL)
		if c.Group == "" {
			c.Group = "默认"
		}
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
	m.mu.Unlock()

	logger.LogPrintf("✅ [player] 订阅加载完成: %d 频道 / %d 分组", len(newOrder), len(newGroups))

	// EPG：M3U XMLTV 由服务端拉取解析
	if epgSrc.Type == "xml" && epgSrc.URL != "" {
		go m.epg.Load(epgSrc.URL)
		m.epg.startRefresh(epgSrc.URL, m.cfg.UpdateInterval)
	}
}

// fetch 支持本地文件路径与 http(s) URL。
func (m *Manager) fetch(src string) []byte {
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		req, err := http.NewRequest(http.MethodGet, src, nil)
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

// assignKey 生成稳定不透明 key（md5 前 8 + sha1 前 4 = 12 hex），冲突时加盐。
func (m *Manager) assignKey(rawURL string, used map[string]string) string {
	h1 := md5.Sum([]byte(rawURL))
	h2 := sha1.Sum([]byte(rawURL))
	key := hex.EncodeToString(h1[:])[:8] + hex.EncodeToString(h2[:])[:4]
	// 若 key 已被占用且不同源，追加递增后缀
	i := 0
	for {
		k := key
		if i > 0 {
			k = fmt.Sprintf("%s%d", key, i)
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

func (m *Manager) EPG() *EPGBank {
	return m.epg
}
