package player

// 台标死链探测：模板补齐 / 源自带的外部台标地址可能根本没有对应图片
// （地方台、小众台在公共图床常常没有收录），前端只能逐个 <img> 请求
// 等失败才回落首字徽章，还挤占首屏连接。服务端并发探测，确认 404/410
// 的地址直接置空——下发给前端的 logo 地址即真相。
//
// 探测语义（宁可多留、不可误杀）：
//   - 404 / 410        → 确认缺失，置空并短 TTL 缓存（图床可能后续补图）
//   - 2xx（含忽略 Range 回全量 200）→ 确认存在，长 TTL 缓存
//   - 403 / 5xx / 网络错误 / 重定向异常 → 不确定：保留原地址且不写缓存，
//     可能是防盗链/风控对服务端 IP 的差异对待，浏览器端仍可能加载成功
//
// 时序（不阻塞订阅重载——APK 冷启动曾在这里同步等满探测预算，频道表迟迟
// 不能下发，表现为"启动慢"）：
//   - 缓存命中的结论（含死链）同步应用：零网络开销，聚合前语义保持；
//   - 缓存未命中的地址交后台探测（单飞 + 放宽预算），探完后先把结果落盘
//     再清当前在播频道表里的死链。重载不等它，频道表先下发。
//
// 探测结论持久化到配置文件同目录（logo_probe.json）：进程重启/APK 重启
// 不丢结论，稳态零探测、死链也不会随重启"复活"再闪 404。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	httpclient "github.com/qist/tvgate/utils/http"
)

const (
	logoProbeConcurrency = 16              // 并发探测数：目标多为同 Host 图床，keep-alive 连接池足够吞吐
	logoProbeTimeout     = 5 * time.Second // 单请求超时
	// 后台探测不在任何关键路径上，预算放宽：图床慢（实测单请求 ~0.5-0.8s）、
	// 地址多（线上实测 984 个唯一 URL）时一轮尽量多清。
	logoProbeAsyncBudget = 30 * time.Second
	logoProbeHitTTL      = 24 * time.Hour // 命中：图床有图，长缓存
	logoProbeMissTTL     = 2 * time.Hour  // 确认缺失：图床可能后续补图，短缓存
)

// logoProbeCache: url -> logoProbeEntry。包级存储：跨 Manager 重建/热重载存活。
var logoProbeCache sync.Map

type logoProbeEntry struct {
	Exists bool      `json:"exists"`
	At     time.Time `json:"at"`
}

func logoProbeTTL(exists bool) time.Duration {
	if exists {
		return logoProbeHitTTL
	}
	return logoProbeMissTTL
}

// logoProbeClient 探测专用 client：项目 DNS 解析链 + 跟随重定向（≤5 跳），
// 包级单例复用连接池。
var (
	logoProbeOnce   sync.Once
	logoProbeClient *http.Client
)

func getLogoProbeClient() *http.Client {
	logoProbeOnce.Do(func() {
		c := httpclient.NewHTTPClient(&config.Cfg, nil)
		c.Timeout = logoProbeTimeout
		c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		}
		logoProbeClient = c
	})
	return logoProbeClient
}

// probeLogoExists 探测单个台标地址，返回 (是否存在, 是否有确定结论)。
// 用 GET + Range 取 1 字节而非 HEAD：部分图床不支持 HEAD（405/行为不一致），
// 忽略 Range 回 200 也不影响判定。
func probeLogoExists(client *http.Client, ua, rawURL string) (exists bool, decided bool) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return true, false
	}
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	req.Header.Set("Range", "bytes=0-0")
	resp, err := client.Do(req)
	if err != nil {
		return true, false
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
		_ = resp.Body.Close()
	}()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return false, true
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return true, true
	default:
		return true, false
	}
}

// pruneDeadLogos 置空频道里确认 404/410 的外部台标地址。
// 调用时机在组内聚合之前：缓存命中的死链先清掉，聚合时头部 TVGLogo 才能
// 落到存活的线路。缓存未命中的地址交后台探测（不阻塞重载），探完后另行
// 清理在播频道表（见 probeAndPrune）。
func (m *Manager) pruneDeadLogos(chans []*Channel) {
	loadLogoProbeCache()
	// 收集外部 http(s) 台标地址；/player/logo/ 本地路径与相对路径不在范围
	// （本地 logo_dir 在填充时已验证文件存在）。
	urls := make(map[string]bool, len(chans))
	for _, c := range chans {
		u := c.TVGLogo
		if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
			urls[u] = true
		}
	}
	if len(urls) == 0 {
		return
	}

	// 缓存有效期内的直接取结论；未缓存/过期的交后台探测
	now := time.Now()
	dead := make(map[string]bool)
	pending := make([]string, 0, len(urls))
	cached := 0
	for u := range urls {
		if e, ok := logoProbeCache.Load(u); ok {
			if ent, ok2 := e.(logoProbeEntry); ok2 && now.Sub(ent.At) < logoProbeTTL(ent.Exists) {
				cached++
				if !ent.Exists {
					dead[u] = true
				}
				continue
			}
		}
		pending = append(pending, u)
	}
	if len(dead) > 0 {
		cleared := 0
		for _, c := range chans {
			if dead[c.TVGLogo] {
				c.TVGLogo = ""
				cleared++
			}
		}
		logger.LogPrintf("[player] 台标死链清理: 置空 %d 个频道（缓存命中 %d / 外部地址 %d / 待探测 %d）",
			cleared, cached, len(urls), len(pending))
	}
	if len(pending) == 0 {
		return
	}
	// 探测 client 在同步段就绪（包级 Once 只构造一次）：后台 goroutine 不再
	// 读全局配置（与重载/配置热更新写入竞争）。UA 同理，派发前取好。
	getLogoProbeClient()
	ua := m.DefaultUA()
	if logoProbeAsyncDisabled {
		return // 测试环境：不派发后台轮，避免跨测试 goroutine 读被换掉的配置
	}
	// 后台探测：单飞——已有探测轮在进行时直接返回，剩余地址由下一轮重载续探。
	logoProbeRoundMu.Lock()
	if logoProbeRoundActive {
		logoProbeRoundMu.Unlock()
		return
	}
	logoProbeRoundActive = true
	logoProbeRoundMu.Unlock()
	go m.probeAndPrune(pending, ua)
}

// logoProbeAsyncDisabled 测试开关：置位时 pruneDeadLogos 不派发后台探测轮
// （测试会整体替换全局配置，泄漏的后台 goroutine 读到换掉的配置会触发
// data race 报告；后台路径本身由 TestProbeAndPruneClearsLive 直接验证）。
var logoProbeAsyncDisabled bool

var (
	logoProbeRoundMu     sync.Mutex
	logoProbeRoundActive bool
)

// probeAndPrune 后台探测一轮未缓存地址：结论落盘后，清当前在播频道表里
// 新确认的死链。频道表此刻可能已被这次重载发布（探测不等它），所以对
// m.order 现值清理；期间若发生再次重载，m.order 换成新指针也无碍——死链
// 是按 URL 判定的，新一轮重载的同步段会用刚落盘的缓存直接清。
func (m *Manager) probeAndPrune(pending []string, ua string) {
	defer func() {
		logoProbeRoundMu.Lock()
		logoProbeRoundActive = false
		logoProbeRoundMu.Unlock()
	}()
	probed := m.probeLogoURLs(pending, logoProbeAsyncBudget, ua)
	if len(probed) == 0 {
		return
	}
	saveLogoProbeCache()
	dead := make(map[string]bool, len(probed))
	for u, exists := range probed {
		if !exists {
			dead[u] = true
		}
	}
	if len(dead) == 0 {
		return
	}
	m.mu.Lock()
	cleared := 0
	for _, c := range m.order {
		if dead[c.TVGLogo] {
			c.TVGLogo = ""
			cleared++
		}
	}
	m.mu.Unlock()
	logger.LogPrintf("[player] 台标后台探测完成: 探测 %d / 置空 %d 个频道（结论已落盘）",
		len(probed), cleared)
}

// probeLogoURLs 限并发 + 总预算地批量探测；只返回已有确定结论的 url→exists。
// 预算耗尽或单请求失败的地址不返回（视为不确定，下轮续探）。
func (m *Manager) probeLogoURLs(urls []string, budget time.Duration, ua string) map[string]bool {
	if len(urls) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	client := getLogoProbeClient()

	type result struct {
		url    string
		exists bool
	}
	jobCh := make(chan string)
	resCh := make(chan result, len(urls)) // 有界缓冲：worker 发送永不阻塞
	var wg sync.WaitGroup
	workers := min(logoProbeConcurrency, len(urls))
	for range workers {
		wg.Go(func() {
			for u := range jobCh {
				if ctx.Err() != nil {
					return
				}
				exists, decided := probeLogoExists(client, ua, u)
				if decided {
					logoProbeCache.Store(u, logoProbeEntry{Exists: exists, At: time.Now()})
					resCh <- result{url: u, exists: exists}
				}
			}
		})
	}
	go func() {
		defer close(jobCh)
		for _, u := range urls {
			select {
			case jobCh <- u:
			case <-ctx.Done():
				return
			}
		}
	}()
	wg.Wait()
	close(resCh)

	out := make(map[string]bool, len(urls))
	for r := range resCh {
		out[r.url] = r.exists
	}
	return out
}

// ---- 探测结论持久化（配置文件同目录 logo_probe.json）-----------------------

// logoProbeFile 是 logo_probe.json 的落盘形态。
type logoProbeFile struct {
	Entries map[string]logoProbeEntry `json:"entries"`
}

func logoProbeCachePath() string {
	if config.ConfigFilePath == nil || *config.ConfigFilePath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(*config.ConfigFilePath), "logo_probe.json")
}

var logoProbeLoadOnce sync.Once

// loadLogoProbeCache 进程内首次用到探测缓存时从磁盘恢复（重启/APK 重启不
// 重复探测：结论直接生效，死链不会随重启闪回）。过期条目丢弃。
func loadLogoProbeCache() {
	logoProbeLoadOnce.Do(func() {
		restoreLogoProbeCache(logoProbeCachePath())
	})
}

// restoreLogoProbeCache 从磁盘读取未过期结论装回缓存；文件缺失/损坏静默跳过。
func restoreLogoProbeCache(p string) {
	if p == "" {
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return // 首次运行无文件
	}
	var f logoProbeFile
	if err := json.Unmarshal(b, &f); err != nil {
		logger.LogPrintf("⚠️ [player] 台标探测缓存损坏，忽略: %v", err)
		return
	}
	now := time.Now()
	for u, e := range f.Entries {
		if now.Sub(e.At) >= logoProbeHitTTL {
			continue
		}
		logoProbeCache.Store(u, e)
	}
}

var logoProbeSaveMu sync.Mutex

// saveLogoProbeCache 把未过期结论落盘（临时文件 + rename 原子替换）。
func saveLogoProbeCache() {
	p := logoProbeCachePath()
	if p == "" {
		return
	}
	logoProbeSaveMu.Lock()
	defer logoProbeSaveMu.Unlock()
	now := time.Now()
	f := logoProbeFile{Entries: make(map[string]logoProbeEntry, 64)}
	logoProbeCache.Range(func(k, v any) bool {
		if e, ok := v.(logoProbeEntry); ok && now.Sub(e.At) < logoProbeHitTTL {
			f.Entries[k.(string)] = e
		}
		return true
	})
	b, err := json.Marshal(f)
	if err != nil {
		return
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return
	}
	_ = os.Rename(tmp, p)
}
