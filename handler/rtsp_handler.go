package handler

import (
	// "bytes"
	"context"
	"fmt"
	// "github.com/asticode/go-astits"
	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/base"
	"github.com/bluenviron/gortsplib/v4/pkg/description"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
	// "github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	// "github.com/pion/rtp"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/lb"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/proxy"
	"github.com/qist/tvgate/rules"
	// "github.com/qist/tvgate/utils/buffer"
	"github.com/qist/tvgate/stream"
	"net"
	"net/http"
	"net/url"
	"strings"
	// "sync"
	// "sync/atomic"
	"time"
)

func RtspToHTTPHandler(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/rtsp/")
	if path == "" {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	// logger.LogPrintf("RTSP → HTTP request: %s", path)
	parts := strings.SplitN(path, "/", 2)
	hostPort := parts[0]
	if !strings.Contains(hostPort, ":") {
		hostPort = hostPort + ":554"
	}
	streamPath := ""
	if len(parts) > 1 {
		streamPath = "/" + parts[1]
	}
	rtspURL := fmt.Sprintf("rtsp://%s%s", hostPort, streamPath)
	if r.URL.RawQuery != "" {
		rtspURL += "?" + r.URL.RawQuery
	}
	logger.LogPrintf("RTSP → HTTP request: %s", rtspURL)

	parsedURL, err := url.Parse(rtspURL)
	if err != nil {
		http.Error(w, "URL parse error: "+err.Error(), 500)
		return
	}

	transport := gortsplib.TransportTCP
	client := &gortsplib.Client{
		Transport: &transport,
	}
	defer client.Close()
	hostname := parsedURL.Hostname()
	originalHost := rules.ExtractOriginalDomain(hostPort) // 你自己实现
	if originalHost == "" {
		originalHost = r.URL.Query().Get("original_host")
	}
	// logger.LogPrintf("hongme %s ooooooo %s", hostname, originalHost)
	// ==== 新增：优先走代理组 ====
	// ==== 新增：优先走代理组（异步选择） ====
	pg := rules.ChooseProxyGroup(hostname, originalHost)
	if pg != nil {
		selectedProxyChan := make(chan *config.ProxyConfig, 1)
		go func() {
			selectedProxyChan <- lb.SelectProxy(pg, rtspURL, false)
		}()

		select {
		case selectedProxy := <-selectedProxyChan:
			if selectedProxy != nil {
				proxyDialer, err := proxy.CreateProxyDialer(*selectedProxy)
				if err == nil {
					client.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
						return proxyDialer.DialContext(ctx, network, addr)
					}
					logger.LogPrintf("RTSP 通过代理 %s://%s:%d", selectedProxy.Type, selectedProxy.Server, selectedProxy.Port)
				} else {
					logger.LogPrintf("RTSP 代理拨号器创建失败，降级直连: %v", err)
				}
			} else {
				logger.LogPrintf("未找到可用代理，使用直连")
			}
		case <-time.After(config.DefaultDialTimeout):
			logger.LogPrintf("选择代理超时，降级直连")
		}
	}
	// ==== 没代理就直连 ====

	// 连接RTSP服务器
	err = client.Start(parsedURL.Scheme, parsedURL.Host)
	if err != nil {
		http.Error(w, "RTSP connect error: "+err.Error(), 500)
		return
	}

	// ⚠️ 这里强制用 TCP 传输
	rtspBaseURL, err := base.ParseURL(rtspURL)
	if err != nil {
		http.Error(w, "RTSP base.ParseURL error: "+err.Error(), 500)
		return
	}
	desc, _, err := client.Describe(rtspBaseURL)
	if err != nil {
		http.Error(w, "RTSP describe error: "+err.Error(), 500)
		return
	}
	medias := desc.Medias
	baseURL := desc.BaseURL

	var (
		videoMedia   *description.Media
		videoFormat  *format.H264
		mpegtsFormat *format.MPEGTS
		audioMedia   *description.Media
		audioFormat  *format.MPEG4Audio
	)

	// for _, m := range medias {
	// 	logger.LogPrintf("媒体类型: %v", m)
	// 	for _, f := range m.Formats {
	// 		logger.LogPrintf("Format: %T", f)
	// 	}
	// }

	for _, m := range medias {
		for _, f := range m.Formats {
			switch f2 := f.(type) {
			case *format.H264:
				if videoMedia == nil {
					_, err = client.Setup(baseURL, m, 0, 0)
					if err == nil {
						videoMedia = m
						videoFormat = f2
					}
				}
			case *format.MPEGTS:
				if videoMedia == nil {
					_, err = client.Setup(baseURL, m, 0, 0)
					if err == nil {
						videoMedia = m
						mpegtsFormat = f2
					}
				}
			case *format.MPEG4Audio:
				if audioMedia == nil {
					_, err = client.Setup(baseURL, m, 0, 0)
					if err == nil {
						audioMedia = m
						audioFormat = f2
					}
				}
			}
		}
	}

	if videoMedia == nil || (videoFormat == nil && mpegtsFormat == nil) {
		http.Error(w, "No supported video stream found", 500)
		return
	}
	ctx := r.Context()
	if mpegtsFormat != nil {
		if err := stream.HandleMpegtsStream(ctx, w, client, videoMedia, mpegtsFormat, r, rtspURL); err != nil {
			http.Error(w, "Stream error: "+err.Error(), 500)
		}
		return
	}

	if err := stream.HandleH264AacStream(ctx, w, client, videoMedia, videoFormat, audioMedia, audioFormat, r, rtspURL); err != nil {
		http.Error(w, "Stream error: "+err.Error(), 500)
	}
}
