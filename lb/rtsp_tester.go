package lb

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/base"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
	"github.com/qist/tvgate/config"
	p "github.com/qist/tvgate/proxy"
)

func TestRTSPProxy(proxy config.ProxyConfig, rtspURL string) (time.Duration, error) {
	proxyDialer, err := p.CreateProxyDialer(proxy)
	if err != nil {
		return 0, fmt.Errorf("创建代理失败: %w", err)
	}

	// 解析 RTSP 地址
	parsed, err := base.ParseURL(rtspURL)
	if err != nil {
		return 0, fmt.Errorf("解析RTSP地址失败: %w", err)
	}

	// 设置 RTSP 客户端，走代理 Dialer
	transport := gortsplib.TransportTCP
	client := &gortsplib.Client{
		Transport: &transport,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return proxyDialer.DialContext(ctx, network, addr)
		},
	}
	defer client.Close()

	start := time.Now()

	// 连接 RTSP 服务端
	err = client.Start(parsed.Scheme, parsed.Host)
	if err != nil {
		return 0, fmt.Errorf("RTSP握手失败: %w", err)
	}

	// 获取媒体信息
	session, _, err := client.Describe(parsed)
	if err != nil {
		return 0, fmt.Errorf("DESCRIBE失败: %w", err)
	}

	// 判断是否存在支持的视频流
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
		return 0, fmt.Errorf("未发现支持的视频流（无 H264/H265/VP8/VP9/MPEGTS）")
	}

	// SETUP 每一路媒体
	for _, m := range session.Medias {
		_, err := client.Setup(session.BaseURL, m, 0, 0)
		if err != nil {
			return 0, fmt.Errorf("SETUP失败: %w", err)
		}
	}

	// PLAY 开始播放
	_, err = client.Play(nil)
	if err != nil {
		return 0, fmt.Errorf("PLAY失败: %w", err)
	}

	return time.Since(start), nil
}
