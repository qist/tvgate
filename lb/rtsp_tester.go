package lb

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/qist/tvgate/config"
	p "github.com/qist/tvgate/proxy"
)

// TestRTSPProxy 测试代理访问 RTSP 流（OPTIONS + DESCRIBE + SETUP + PLAY）
func TestRTSPProxy(proxy config.ProxyConfig, rtspURL string) (time.Duration, error) {
	// fmt.Printf("DEBUG TestRTSPProxy: proxy=%s://%s:%d, rtspURL=%s\n", proxy.Type, proxy.Server, proxy.Port, rtspURL)

	// 创建代理 Dialer
	proxyDialer, err := p.CreateProxyDialer(proxy)
	if err != nil {
		return 0, fmt.Errorf("创建代理失败: %w", err)
	}

	// 解析 RTSP URL
	parsedURL, err := base.ParseURL(rtspURL)
	if err != nil {
		return 0, fmt.Errorf("解析 RTSP 地址失败: %w", err)
	}
	// if parsedURL.Scheme != "rtsp" {
	// 	return 0, fmt.Errorf("不支持的 scheme: %s", parsedURL.Scheme)
	// }

	// fmt.Printf("DEBUG parsed URL: scheme=%s, host=%s, path=%s\n", parsedURL.Scheme, parsedURL.Host, parsedURL.Path)

	// 创建 RTSP 客户端
	protocol := gortsplib.ProtocolTCP
	client := &gortsplib.Client{
		Scheme:                     parsedURL.Scheme,
		Host:                       parsedURL.Host,
		Protocol:                   &protocol,
		AnyPortEnable:              true,
		DisableRTCPSenderReports:   true,
		DisableRTCPReceiverReports: true,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// fmt.Printf("DEBUG DialContext: network=%s, addr=%s\n", network, addr)
			return proxyDialer.DialContext(ctx, network, addr)
		},
	}
	defer client.Close()

	start := time.Now()

	// 连接 RTSP 服务端
	if err := client.Start(); err != nil {
		return 0, fmt.Errorf("RTSP握手失败: %w", err)
	}

	// 执行 OPTIONS 请求
	optionsURL := *parsedURL
	_, err = client.Options(&optionsURL)
	if err != nil {
		// fmt.Printf("DEBUG OPTIONS error: %v\n", err)
		return 0, fmt.Errorf("OPTIONS失败: %w", err)
	}
	// fmt.Println("DEBUG RTSP OPTIONS请求成功")

	// 执行 DESCRIBE 获取媒体信息
	session, _, err := client.Describe(parsedURL)
	if err != nil {
		// fmt.Printf("DEBUG DESCRIBE error: %v\n", err)
		return 0, fmt.Errorf("DESCRIBE失败: %w", err)
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
		return 0, fmt.Errorf("未发现支持的视频流（无 H264/H265/VP8/VP9/MPEGTS）")
	}
	// fmt.Println("DEBUG DESCRIBE成功，发现支持的视频流")

	// SETUP 所有媒体
	if err := client.SetupAll(session.BaseURL, session.Medias); err != nil {
		return 0, fmt.Errorf("SETUP失败: %w", err)
	}
	// fmt.Println("DEBUG SETUP成功")

	// PLAY
	if _, err := client.Play(nil); err != nil {
		return 0, fmt.Errorf("PLAY失败: %w", err)
	}
	// fmt.Println("DEBUG PLAY成功")

	return time.Since(start), nil
}
