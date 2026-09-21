package player

// 台标死链探测：模板补齐 / 源自带的外部台标地址可能根本没有对应图片
// （地方台、小众台在公共图床常常没有收录），前端只能逐个 <img> 请求
// 等失败才回落首字徽章，还挤占首屏连接。订阅重载时服务端并发探测一次，
// 确定性 404/410 的地址直接置空——下发给前端的 logo 地址即真相。
//
// 探测语义（宁可多留、不可误杀）：
//   - 404 / 410        → 确认缺失，置空并短 TTL 缓存（图床可能后续补图）
//   - 2xx（含忽略 Range 回全量 200）→ 确认存在，长 TTL 缓存
//   - 403 / 5xx / 网络错误 / 重定向异常 → 不确定：保留原地址且不写缓存，
//     可能是防盗链/风控对服务端 IP 的差异对待，浏览器端仍可能加载成功
//
// 单轮探测有总预算（logoProbeBudget）：图床黑洞时不会无限拖住订阅重载，
// 未探完的地址本轮原样保留、下轮续探；结果缓存跨重载累积，稳态零探测。

import (
	"context"
	"fmt"
	"io"
	"net/http"
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
	logoProbeBudget      = 10 * time.Second
	logoProbeHitTTL      = 24 * time.Hour // 命中：图床有图，长缓存
	logoProbeMissTTL     = 2 * time.Hour  // 确认缺失：图床可能后续补图，短缓存
)

// logoProbeCache: url -> logoProbeEntry。包级存储：跨 Manager 重建/热重载存活。
var logoProbeCache sync.Map

type logoProbeEntry struct {
	exists bool
	at     time.Time
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

// pruneDeadLogos 并发探测频道的外部台标地址，确认 404/410 的原地置空。
// 在组内聚合之前调用：死链先清掉，聚合时头部 TVGLogo 才能落到存活的线路。
// 未探完（预算耗尽）的地址本轮原样保留，缓存跨重载累积，稳态零探测。
func (m *Manager) pruneDeadLogos(chans []*Channel) {
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

	// 缓存有效期内的直接取结论；未缓存/过期的进探测队列
	now := time.Now()
	dead := make(map[string]bool)
	pending := make([]string, 0, len(urls))
	cached := 0
	for u := range urls {
		if e, ok := logoProbeCache.Load(u); ok {
			if ent, ok2 := e.(logoProbeEntry); ok2 && now.Sub(ent.at) < logoProbeTTL(ent.exists) {
				cached++
				if !ent.exists {
					dead[u] = true
				}
				continue
			}
		}
		pending = append(pending, u)
	}

	probed := m.probeLogoURLs(pending)
	for u, exists := range probed {
		if !exists {
			dead[u] = true
		}
	}
	if len(dead) == 0 {
		return
	}
	cleared := 0
	for _, c := range chans {
		if dead[c.TVGLogo] {
			c.TVGLogo = ""
			cleared++
		}
	}
	logger.LogPrintf("[player] 台标死链清理: 置空 %d 个频道（本轮探测 %d / 缓存命中 %d / 外部地址 %d）",
		cleared, len(probed), cached, len(urls))
}

// probeLogoURLs 限并发 + 总预算地批量探测；只返回已有确定结论的 url→exists。
// 预算耗尽或单请求失败的地址不返回（视为不确定，下轮续探）。
func (m *Manager) probeLogoURLs(urls []string) map[string]bool {
	if len(urls) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), logoProbeBudget)
	defer cancel()
	client := getLogoProbeClient()
	ua := m.DefaultUA()

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
					logoProbeCache.Store(u, logoProbeEntry{exists: exists, at: time.Now()})
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
