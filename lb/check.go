package lb

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/qist/tvgate/config"
	p "github.com/qist/tvgate/proxy"
)

// CheckOutcome 是单次直播源探测结果，HTTP 与 RTSP 共用。
type CheckOutcome struct {
	ResponseTime time.Duration
	StatusCode   int
	Err          error
}

// CheckTarget 复用 lb 选路自身的直播检测逻辑，供代理池探测与请求时测速共用：
//   - rtsp://    → OPTIONS + DESCRIBE + SETUP + PLAY（TestRTSPProxyWithUA）
//   - http(s):// → 经代理发 Range 请求，只读前 2KB
//
// 注意：Err 仅表示连接/请求层面的失败，HTTP 状态码交给调用方按场景判定
// （lb 选路保持 <500 视为可连；池探测要求 <400，避免把错误页当可用节点）。
func CheckTarget(ctx context.Context, proxyConfig config.ProxyConfig, targetURL string, ipv6 bool, ua string) CheckOutcome {
	if strings.HasPrefix(strings.ToLower(targetURL), "rtsp://") {
		rt, err := TestRTSPProxyWithUA(ctx, proxyConfig, targetURL, ua)
		return CheckOutcome{ResponseTime: rt, StatusCode: http.StatusOK, Err: err}
	}
	return checkHTTPTarget(ctx, proxyConfig, targetURL, ipv6, ua)
}

func checkHTTPTarget(ctx context.Context, proxyConfig config.ProxyConfig, targetURL string, ipv6 bool, ua string) CheckOutcome {
	client, err := p.CreateProxyClient(ctx, &config.Cfg, proxyConfig, ipv6)
	if err != nil {
		return CheckOutcome{Err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return CheckOutcome{Err: err}
	}
	// 空字符串会让 Go 省略 User-Agent 头：探测时不固定成浏览器 UA，
	// 避免被「禁止浏览器 UA」的直播源直接拒绝。
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Range", "bytes=0-2047")

	start := time.Now()
	resp, err := client.Do(req)
	out := CheckOutcome{ResponseTime: time.Since(start), Err: err}
	if resp != nil {
		out.StatusCode = resp.StatusCode
		resp.Body.Close()
	}
	return out
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
