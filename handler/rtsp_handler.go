package handler

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"

	"github.com/qist/tvgate/auth"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/lb"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/monitor"
	"github.com/qist/tvgate/proxy"
	"github.com/qist/tvgate/rules"
	"github.com/qist/tvgate/stream"
	tsync "github.com/qist/tvgate/utils/sync"
)

func RtspToHTTPHandler(w http.ResponseWriter, r *http.Request) {
	clientIP := monitor.GetClientIP(r)
	connID := clientIP + "_" + strconv.FormatInt(time.Now().UnixNano(), 10)

	// 全局 token 验证
	if auth.GetGlobalTokenManager() != nil {
		tokenParam := "my_token"
		if auth.GetGlobalTokenManager().TokenParamName != "" {
			tokenParam = auth.GetGlobalTokenManager().TokenParamName
		}
		token := r.URL.Query().Get(tokenParam)
		if !auth.GetGlobalTokenManager().ValidateToken(token, r.URL.Path, connID) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		auth.GetGlobalTokenManager().KeepAlive(token, connID, clientIP, r.URL.Path)
	}

	path := strings.TrimPrefix(r.URL.Path, "/rtsp/")
	if path == "" {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	parts := strings.SplitN(path, "/", 2)
	hostPort := parts[0]
	if !strings.Contains(hostPort, ":") {
		hostPort += ":554"
	}
	streamPath := ""
	if len(parts) > 1 {
		streamPath = "/" + parts[1]
	}

	rtspURL := fmt.Sprintf("rtsp://%s%s", hostPort, streamPath)
	if r.URL.RawQuery != "" {
		if auth.GetGlobalTokenManager() != nil {
			tokenParam := "my_token"
			if auth.GetGlobalTokenManager().TokenParamName != "" {
				tokenParam = auth.GetGlobalTokenManager().TokenParamName
			}
			// 去掉 token
			query := r.URL.RawQuery
			newParts := []string{}
			for _, kv := range strings.Split(query, "&") {
				if !strings.HasPrefix(kv, tokenParam+"=") {
					newParts = append(newParts, kv)
				}
			}
			if len(newParts) > 0 {
				rtspURL += "?" + strings.Join(newParts, "&")
			}
		} else {
			rtspURL += "?" + r.URL.RawQuery
		}
	}

	logger.LogPrintf("RTSP → HTTP request: %s", rtspURL)
	parsedURL, err := url.Parse(rtspURL)
	if err != nil {
		http.Error(w, "URL parse error: "+err.Error(), 500)
		return
	}

	monitor.ActiveClients.Register(connID, &monitor.ClientConnection{
		IP:             clientIP,
		URL:            rtspURL,
		UserAgent:      r.UserAgent(),
		ConnectionType: "RTSP",
		ConnectedAt:    time.Now(),
		LastActive:     time.Now(),
	})
	defer monitor.ActiveClients.Unregister(connID, "RTSP")

	client := &gortsplib.Client{
		Scheme:        parsedURL.Scheme,
		Host:          parsedURL.Host,
		AnyPortEnable: true,
		Protocol: func() *gortsplib.Protocol {
			t := gortsplib.ProtocolTCP
			return &t
		}(),
		DisableRTCPSenderReports:   true,
		DisableRTCPReceiverReports: true,
	}

	// 代理组选择
	hostname := parsedURL.Hostname()
	originalHost := rules.ExtractOriginalDomain(hostPort)
	if originalHost == "" {
		originalHost = r.URL.Query().Get("original_host")
	}

	pg := rules.ChooseProxyGroup(hostname, originalHost)
	if pg != nil {
		// 使用请求上下文，以便客户端断开时能取消代理选择
		proxyCtx, proxyCancel := context.WithCancel(r.Context())
		defer proxyCancel()

		selectedProxyChan := make(chan *config.ProxyConfig, 1)
		var wg tsync.WaitGroup
		wg.Go(func() {
			selectedProxyChan <- lb.SelectProxy(proxyCtx, pg, rtspURL, false)
		})

		select {
		case selectedProxy := <-selectedProxyChan:
			if selectedProxy != nil {
				proxyDialer, err := proxy.CreateProxyDialer(*selectedProxy)
				if err == nil {
					client.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
						return proxyDialer.DialContext(ctx, network, addr)
					}
					logger.LogPrintf("RTSP 通过代理 %s://%s:%d", selectedProxy.Type, selectedProxy.Server, selectedProxy.Port)
				}
			}
		case <-time.After(config.DefaultDialTimeout):
			proxyCancel() // 超时立即取消后台任务
			logger.LogPrintf("选择代理超时，降级直连")
		}
	}

	hub := stream.GetOrCreateHubs(rtspURL)

	var (
		videoMedia   *description.Media
		videoFormat  *format.H264
		mpegtsFormat *format.MPEGTS
		audioMedia   *description.Media
		audioFormat  *format.MPEG4Audio
	)

	existingClient := hub.GetRtspClient()
	if existingClient != nil {
		client = existingClient
		storedVideoMedia, storedVideoFormat, storedAudioMedia, storedAudioFormat := hub.GetMediaInfo()
		if storedVideoMedia != nil {
			videoMedia = storedVideoMedia
		}
		if f, ok := storedVideoFormat.(*format.H264); ok {
			videoFormat = f
		} else if f, ok := storedVideoFormat.(*format.MPEGTS); ok {
			mpegtsFormat = f
		}
		if storedAudioMedia != nil {
			audioMedia = storedAudioMedia
		}
		if storedAudioFormat != nil {
			audioFormat = storedAudioFormat
		}
	} else {
		err = client.Start()
		if err != nil {
			http.Error(w, "RTSP connect error: "+err.Error(), 500)
			return
		}

		// Ensure client is closed if setup fails
		setupSuccess := false
		defer func() {
			if !setupSuccess {
				client.Close()
			}
		}()

		// // 用 parsedURL 构造 *base.URL 用于 Setup
		// setupURL := &base.URL{
		// 	Scheme:   parsedURL.Scheme,
		// 	Host:     parsedURL.Host,
		// 	Path:     parsedURL.Path,
		// 	RawQuery: parsedURL.RawQuery,
		// }
		parsedURL, err := base.ParseURL(rtspURL)
		_, err = client.Options(parsedURL)
		if err != nil {
			http.Error(w, "RTSP OPTIONS error: "+err.Error(), 500)
			return
		}

		desc, _, err := client.Describe(parsedURL)
		if err != nil {
			http.Error(w, "RTSP DESCRIBE error: "+err.Error(), 500)
			return
		}
		for _, m := range desc.Medias {
			for _, f := range m.Formats {
				switch f2 := f.(type) {
				case *format.H264:
					if videoMedia == nil {
						_, err = client.Setup(parsedURL, m, 0, 0)
						if err == nil {
							videoMedia = m
							videoFormat = f2
						}
					}
				case *format.MPEGTS:
					if videoMedia == nil {
						_, err = client.Setup(parsedURL, m, 0, 0)
						if err == nil {
							videoMedia = m
							mpegtsFormat = f2
						}
					}
				case *format.MPEG4Audio:
					if audioMedia == nil {
						_, err = client.Setup(parsedURL, m, 0, 0)
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

		if mpegtsFormat != nil {
			hub.SetMediaInfo(videoMedia, mpegtsFormat)
		} else {
			hub.SetMediaInfo(videoMedia, videoFormat)
		}
		if audioFormat != nil {
			hub.SetAudioMediaInfo(audioMedia, audioFormat)
		}
		hub.SetRtspClient(client)
		setupSuccess = true
	}

	ctx := r.Context()
	updateActive := func() {
		monitor.ActiveClients.UpdateLastActive(connID, time.Now())
	}

	if mpegtsFormat != nil && videoMedia != nil {
		if err := stream.HandleMpegtsStream(ctx, w, client, videoMedia, mpegtsFormat, r, rtspURL, hub, updateActive); err != nil {
			http.Error(w, "Stream error: "+err.Error(), 500)
		}
		return
	}

	if videoFormat != nil && videoMedia != nil {
		if err := stream.HandleH264AacStream(ctx, w, client, videoMedia, videoFormat, audioMedia, audioFormat, r, rtspURL, hub, updateActive); err != nil {
			http.Error(w, "Stream error: "+err.Error(), 500)
		}
		return
	}

	http.Error(w, "No supported stream format found", 500)
}
