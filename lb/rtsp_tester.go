package lb

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/pion/rtp"
	"github.com/qist/tvgate/config"
	p "github.com/qist/tvgate/proxy"
)

const (
	// rtspSampleWindow 是 RTSP 握手成功后继续接收 RTP 的采样时长。
	// RTSP 是实时流，2s 通常覆盖一个 GOP，能反映真实码率/出口带宽；
	// 只是统计已推送的 RTP 载荷，不会让服务端额外多发数据。
	rtspSampleWindow = 2 * time.Second
	// rtspMinSampleBytes 小于该样本量时不给吞吐，避免首包关键帧造成虚高。
	rtspMinSampleBytes = 32 << 10
	// rtspSampleGuard 是 ctx 截止前留给关闭连接的余量。
	rtspSampleGuard = 200 * time.Millisecond
)

// TestRTSPProxy 测试代理访问 RTSP 流（OPTIONS + DESCRIBE + SETUP + PLAY）
func TestRTSPProxy(ctx context.Context, proxy config.ProxyConfig, rtspURL string) (time.Duration, error) {
	rt, _, err := testRTSP(ctx, proxy, rtspURL, "", false)
	return rt, err
}

// TestRTSPProxyWithUA 与 TestRTSPProxy 相同，但可指定 User-Agent；
// ua 为空时保持 gortsplib 默认值。只做握手，不采样流量。
func TestRTSPProxyWithUA(ctx context.Context, proxy config.ProxyConfig, rtspURL, ua string) (time.Duration, error) {
	rt, _, err := testRTSP(ctx, proxy, rtspURL, ua, false)
	return rt, err
}

// TestRTSPProxyThroughput 在握手成功后继续接收一段 RTP 流量，
// 返回握手耗时和采样吞吐（字节/秒）；吞吐为 0 表示样本不足。
// 供池探测与请求时测速使用，避免只看握手延迟选到码率不够的节点。
func TestRTSPProxyThroughput(ctx context.Context, proxy config.ProxyConfig, rtspURL, ua string) (time.Duration, int64, error) {
	return testRTSP(ctx, proxy, rtspURL, ua, true)
}

func testRTSP(ctx context.Context, proxy config.ProxyConfig, rtspURL, ua string, sample bool) (time.Duration, int64, error) {
	// 创建代理 Dialer
	proxyDialer, err := p.CreateProxyDialer(proxy)
	if err != nil {
		return 0, 0, fmt.Errorf("创建代理失败: %w", err)
	}

	// 解析 RTSP URL
	parsedURL, err := base.ParseURL(rtspURL)
	if err != nil {
		return 0, 0, fmt.Errorf("解析 RTSP 地址失败: %w", err)
	}

	// 创建 RTSP 客户端
	protocol := gortsplib.ProtocolTCP
	// 探测场景通常带更短的整体超时（pool.probe_timeout），读写超时跟随 ctx，
	// 避免单个不可达节点把探测 goroutine 拖到默认的 15s。
	ioTimeout := config.DefaultDialTimeout
	if deadline, ok := ctx.Deadline(); ok {
		if d := time.Until(deadline); d > 0 && d < ioTimeout {
			ioTimeout = d
		}
	}
	client := &gortsplib.Client{
		Scheme:                   parsedURL.Scheme,
		Host:                     parsedURL.Host,
		Protocol:                 &protocol,
		AnyPortEnable:            true,
		DisableRTCPSenderReports: true,
		UserAgent:                ua,
		ReadTimeout:              ioTimeout,
		WriteTimeout:             ioTimeout,
		DialContext: func(dialCtx context.Context, network, addr string) (net.Conn, error) {
			if ctx != nil {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				default:
				}
			}
			return proxyDialer.DialContext(dialCtx, network, addr)
		},
	}
	defer client.Close()

	start := time.Now()

	// 连接 RTSP 服务端
	if err := client.Start(); err != nil {
		return 0, 0, fmt.Errorf("RTSP握手失败: %w", err)
	}

	// 执行 OPTIONS 请求
	optionsURL := *parsedURL
	if _, err := client.Options(&optionsURL); err != nil {
		return 0, 0, fmt.Errorf("OPTIONS失败: %w", err)
	}

	// 执行 DESCRIBE 获取媒体信息
	session, _, err := client.Describe(parsedURL)
	if err != nil {
		return 0, 0, fmt.Errorf("DESCRIBE失败: %w", err)
	}

	// 检查是否有支持的视频流
	hasVideo := false
	for _, m := range session.Medias {
		for _, f := range m.Formats {
			switch f.(type) {
			case *format.H264, *format.H265, *format.VP8, *format.VP9, *format.MPEGTS:
				hasVideo = true
			}
		}
	}
	if !hasVideo {
		return 0, 0, fmt.Errorf("未发现支持的视频流（无 H264/H265/VP8/VP9/MPEGTS）")
	}

	// SETUP 所有媒体
	if err := client.SetupAll(session.BaseURL, session.Medias); err != nil {
		return 0, 0, fmt.Errorf("SETUP失败: %w", err)
	}

	// 必须在 SETUP 之后、PLAY 之前注册：OnPacketRTPAny 依赖已 setup 的媒体。
	var recvBytes atomic.Int64
	if sample {
		client.OnPacketRTPAny(func(_ *description.Media, _ format.Format, pkt *rtp.Packet) {
			recvBytes.Add(int64(len(pkt.Payload)))
		})
	}

	// PLAY
	if _, err := client.Play(nil); err != nil {
		return 0, 0, fmt.Errorf("PLAY失败: %w", err)
	}

	handshake := time.Since(start)
	if !sample {
		return handshake, -1, nil
	}
	if window := rtspSampleBudget(ctx); window > 0 {
		return handshake, sampleRTPThroughput(ctx, &recvBytes, window), nil
	}
	return handshake, -1, nil
}

// rtspSampleBudget 计算采样窗口：最多 rtspSampleWindow，且不能吃掉 ctx 余量；
// 剩余时间连 500ms 都不到时放弃采样。
func rtspSampleBudget(ctx context.Context) time.Duration {
	window := rtspSampleWindow
	if deadline, ok := ctx.Deadline(); ok {
		if remain := time.Until(deadline) - rtspSampleGuard; remain < window {
			window = remain
		}
	}
	if window < 500*time.Millisecond {
		return 0
	}
	return window
}

// sampleRTPThroughput 等待一个采样窗口，按累计 RTP 载荷计算字节/秒。
func sampleRTPThroughput(ctx context.Context, recv *atomic.Int64, window time.Duration) int64 {
	start := time.Now()
	timer := time.NewTimer(window)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
	elapsed := time.Since(start)
	got := recv.Load()
	if got < rtspMinSampleBytes || elapsed <= 0 {
		return -1
	}
	return int64(float64(got) / elapsed.Seconds())
}
