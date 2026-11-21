package lb

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/qist/tvgate/config"
	p "github.com/qist/tvgate/proxy"
)

const (
	// probeSampleSize 是单次 HTTP 探测最多读取的正文大小。
	// 必须覆盖整个直播分片（5~6MB）：只读 2MB 会正好停在「前段突发」区间，
	// 把「前 3 秒跑满、随后限速甚至直接停住」的出口测成 6Mbps 以上。
	probeSampleSize = 8 << 20
	// probeMinSample 小于该样本量（如 m3u8 播放列表本身）不计算吞吐，
	// 避免把「369 字节日志/播放列表」的耗时误当成带宽。
	probeMinSample = 32 << 10
	// probeTailWindow 吞吐只统计最后这段时间的数据，而不是全程平均：
	// 前段突发不计入，出口一旦限速或停住，后段速率会立刻掉下来。
	probeTailWindow = 3 * time.Second
	// probeKeepSample 只保留正文前这么多字节用于类型判断/播放列表解析，
	// 分片正文不需要整段留在内存里。
	probeKeepSample = 512 << 10
	// probeMaxRedirects 限制探测手工跟随的重定向次数。
	probeMaxRedirects = 5
	// probeMaxPlaylistHops 限制 m3u8 → variant → 分片的最大层数。
	probeMaxPlaylistHops = 2
)

// CheckOutcome 是单次直播源探测结果，HTTP 与 RTSP 共用。
type CheckOutcome struct {
	ResponseTime time.Duration
	StatusCode   int
	// Throughput 是正文采样吞吐（字节/秒）。
	// 0 表示读到了数据但后段停住（真正的跑不动）；
	// -1 表示没有有效样本（正文太小/采样窗口不足），调用方应保留旧值。
	Throughput int64
	Err        error
}

// probeSampleSizeKey 允许单次探测覆盖正文采样上限。
// 池探测对正在播放的节点会调小采样，减少抢播放带宽的量。
type probeSampleSizeKey struct{}

// WithProbeSampleSize 覆盖本次探测的正文采样上限（字节）。
func WithProbeSampleSize(ctx context.Context, n int) context.Context {
	if n <= 0 {
		return ctx
	}
	return context.WithValue(ctx, probeSampleSizeKey{}, n)
}

func sampleSizeFor(ctx context.Context) int {
	if v, ok := ctx.Value(probeSampleSizeKey{}).(int); ok && v > 0 {
		return v
	}
	return probeSampleSize
}

// CheckTarget 复用 lb 选路自身的直播检测逻辑，供代理池探测与请求时测速共用：
//   - rtsp://    → OPTIONS + DESCRIBE + SETUP + PLAY，PLAY 后采样 2s RTP 载荷；
//   - http(s):// → 经代理发 Range 请求，读最多 2MB 正文采样吞吐；
//     若目标是 m3u8，会继续取首个 variant/分片，避免播放列表太小测不出带宽。
//
// 注意：Err 仅表示连接/请求层面的失败，HTTP 状态码交给调用方按场景判定
// （lb 选路保持 <500 视为可连；池探测要求 <400，避免把错误页当可用节点）。
func CheckTarget(ctx context.Context, proxyConfig config.ProxyConfig, targetURL string, ipv6 bool, ua string) CheckOutcome {
	if strings.HasPrefix(strings.ToLower(targetURL), "rtsp://") {
		rt, throughput, err := TestRTSPProxyThroughput(ctx, proxyConfig, targetURL, ua)
		return CheckOutcome{ResponseTime: rt, StatusCode: http.StatusOK, Throughput: throughput, Err: err}
	}
	return checkHTTPTarget(ctx, proxyConfig, targetURL, ipv6, ua)
}

func checkHTTPTarget(ctx context.Context, proxyConfig config.ProxyConfig, targetURL string, ipv6 bool, ua string) CheckOutcome {
	// 探测流量不计入节点「在用带宽」：否则会把自己的正文采样当成播放占用。
	// 标记必须同时用于 client 和每个请求（transport 读的是 req.Context）。
	reqCtx := p.WithNoUsageCounting(ctx)
	client, err := p.CreateProxyClient(reqCtx, &config.Cfg, proxyConfig, ipv6)
	if err != nil {
		return CheckOutcome{Err: err}
	}

	start := time.Now()
	resp, err := probeRequest(reqCtx, client, targetURL, ua)
	if err != nil {
		return CheckOutcome{ResponseTime: time.Since(start), Err: err}
	}
	defer resp.Body.Close()

	out := CheckOutcome{ResponseTime: time.Since(start), StatusCode: resp.StatusCode}
	// 4xx/5xx 是错误页，不读取也不测吞吐；状态码由调用方判定。
	if resp.StatusCode >= 400 {
		return out
	}

	size := sampleSizeFor(ctx)
	sample, _, tp := readProbeSample(resp.Body, size)
	out.Throughput = tp
	if !isPlaylistSample(resp.Header.Get("Content-Type"), sample) {
		return out
	}

	// m3u8 正文通常只有几百字节，读 2MB 也读不满；继续取首个
	// variant/分片做一次采样，否则吞吐永远测不出来。
	base := responseURL(resp)
	next := playlistSampleURI(sample, base)
	for hop := 0; hop < probeMaxPlaylistHops && next != ""; hop++ {
		segResp, segErr := probeRequest(reqCtx, client, next, ua)
		if segErr != nil {
			// 播放列表本身已探测成功，子请求失败不把节点判死。
			break
		}
		segSample, _, segTP := readProbeSample(segResp.Body, size)
		segType := segResp.Header.Get("Content-Type")
		segBase := responseURL(segResp)
		segStatus := segResp.StatusCode
		segResp.Body.Close()
		if segStatus >= 400 {
			break
		}
		if isPlaylistSample(segType, segSample) {
			next = playlistSampleURI(segSample, segBase)
			continue
		}
		if segTP > out.Throughput {
			out.Throughput = segTP
		}
		break
	}
	return out
}

// probeRequest 手工跟随重定向：CreateProxyClient 的 CheckRedirect 返回
// ErrUseLastResponse，不手工跟随时探测只会看到 302，既拿不到正文也测不到吞吐。
func probeRequest(ctx context.Context, client *http.Client, targetURL, ua string) (*http.Response, error) {
	reqURL := targetURL
	for i := 0; i < probeMaxRedirects; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, err
		}
		// 空字符串会让 Go 省略 User-Agent 头：探测时不固定成浏览器 UA，
		// 避免被「禁止浏览器 UA」的直播源直接拒绝。
		req.Header.Set("User-Agent", ua)
		req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", sampleSizeFor(ctx)-1))

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if !isRedirectStatus(resp.StatusCode) {
			return resp, nil
		}
		loc := resp.Header.Get("Location")
		if loc == "" {
			return resp, nil
		}
		base := responseURL(resp)
		if base == nil {
			base, _ = url.Parse(reqURL)
		}
		var next *url.URL
		if base != nil {
			next, err = base.Parse(loc)
		} else {
			next, err = url.Parse(loc)
		}
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		reqURL = next.String()
	}
	return nil, fmt.Errorf("探测重定向次数超过 %d", probeMaxRedirects)
}

func responseURL(resp *http.Response) *url.URL {
	if resp != nil && resp.Request != nil {
		return resp.Request.URL
	}
	return nil
}

func isRedirectStatus(code int) bool {
	switch code {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

// probeMark 记录某个时刻的累计读取字节，用于计算「后段」速率。
type probeMark struct {
	at time.Duration
	n  int64
}

// readProbeSample 读取探测正文，返回保留的样本、耗时与「后段」吞吐。
//
// 后段吞吐 = 最后 probeTailWindow 内读取的字节数 / 窗口时长：
//   - 出口前段突发、随后限速时，全程平均会被突发段抬高，后段不会；
//   - 出口读到一半直接停住（连接不断但不再给数据）时，后段字节为 0，
//     吞吐记为 0，节点会排到最后，不会继续被选中。
//
// 读取到 EOF / size 上限 / ctx 超时（由调用方的 probe_timeout 控制）才结束。
func readProbeSample(r io.Reader, size int) ([]byte, time.Duration, int64) {
	if size <= 0 {
		size = probeSampleSize
	}
	start := time.Now()
	limit := int64(size)
	keep := int64(probeKeepSample)
	buf := make([]byte, 32<<10)
	sample := make([]byte, 0, 64<<10)
	marks := make([]probeMark, 0, 64)
	var total int64
	for total < limit {
		n, err := r.Read(buf)
		if n > 0 {
			if remain := limit - total; int64(n) > remain {
				n = int(remain)
			}
			if int64(len(sample)) < keep {
				take := n
				if int64(take) > keep-int64(len(sample)) {
					take = int(keep - int64(len(sample)))
				}
				sample = append(sample, buf[:take]...)
			}
			total += int64(n)
			marks = append(marks, probeMark{at: time.Since(start), n: total})
		}
		if err != nil {
			break
		}
	}
	elapsed := time.Since(start)
	return sample, elapsed, tailThroughput(total, elapsed, marks)
}

// tailThroughput 只统计最后 probeTailWindow 的速率。
// 总耗时不足一个窗口（高速出口很快读完）时退回全程平均。
// 返回 -1 表示「没有有效样本」（正文太小或读取中断），调用方应保留旧值；
// 返回 0 表示确实读到了数据、但后段没有任何字节（出口停住）。
func tailThroughput(total int64, elapsed time.Duration, marks []probeMark) int64 {
	if total < probeMinSample || elapsed <= 0 {
		return -1
	}
	if elapsed <= probeTailWindow {
		return int64(float64(total) / elapsed.Seconds())
	}
	cutoff := elapsed - probeTailWindow
	var base int64
	for _, m := range marks {
		if m.at <= cutoff {
			base = m.n
		} else {
			break
		}
	}
	tail := total - base
	if tail <= 0 {
		return 0
	}
	return int64(float64(tail) / probeTailWindow.Seconds())
}

func isPlaylistSample(contentType string, sample []byte) bool {
	if strings.Contains(strings.ToLower(contentType), "mpegurl") {
		return true
	}
	head := sample
	if len(head) > 64 {
		head = head[:64]
	}
	head = bytes.TrimLeft(head, " \t\r\n")
	return bytes.HasPrefix(head, []byte("#EXTM3U"))
}

// playlistSampleURI 从 m3u8 里挑一个可用于吞吐采样的地址：
//   - master（#EXT-X-STREAM-INF）：取第一个 variant，避免误选最低码率；
//   - 媒体播放列表：取最后一个分片，直播场景下最新分片最不容易已过期。
//
// 注释里的 EXT-X-KEY/EXT-X-MAP URI 不参与。相对地址按最终响应 URL 解析。
func playlistSampleURI(sample []byte, base *url.URL) string {
	master := bytes.Contains(sample, []byte("#EXT-X-STREAM-INF")) ||
		bytes.Contains(sample, []byte("#EXT-X-I-FRAME-STREAM-INF"))
	var first, last string
	for _, line := range strings.Split(string(sample), "\n") {
		lt := strings.TrimSpace(line)
		if lt == "" || strings.HasPrefix(lt, "#") {
			continue
		}
		ref, err := url.Parse(lt)
		if err != nil {
			continue
		}
		u := ref.String()
		if base != nil {
			u = base.ResolveReference(ref).String()
		}
		if first == "" {
			first = u
		}
		last = u
	}
	if master {
		return first
	}
	return last
}

// GroupProbeUA 解析探测/测速用 User-Agent：
//  1. 代理组 probe_ua；
//  2. 全局 player.ua；
//  3. 空（不发送 User-Agent）。
//
// 仅用于探测，播放请求仍按播放器原逻辑透传，不受影响。
func GroupProbeUA(group *config.ProxyGroupConfig) string {
	if group != nil {
		if ua := strings.TrimSpace(group.ProbeUA); ua != "" {
			return ua
		}
	}
	config.CfgMu.RLock()
	ua := strings.TrimSpace(config.Cfg.Player.UA)
	config.CfgMu.RUnlock()
	return ua
}

// formatThroughput 把字节/秒格式化为带宽文本，供日志与前端一致的展示；
// 0 表示没有有效样本。
func formatThroughput(bytesPerSec int64) string {
	if bytesPerSec <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.1fMbps", float64(bytesPerSec)*8/1e6)
}
