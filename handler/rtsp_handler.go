package handler

import (
	"context"
	"fmt"
	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/base"
	"github.com/bluenviron/gortsplib/v4/pkg/description"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
	"github.com/qist/tvgate/auth"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/lb"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/monitor"
	"github.com/qist/tvgate/proxy"
	"github.com/qist/tvgate/rules"
	"github.com/qist/tvgate/stream"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func RtspToHTTPHandler(w http.ResponseWriter, r *http.Request) {
	// 全局token验证
	clientIP := monitor.GetClientIP(r)
	connID := clientIP + "_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	tokenParamName := "my_token" // 默认参数名
	if auth.GetGlobalTokenManager() != nil {
		// 如果全局配置中有自定义的token参数名，则使用自定义的
		if auth.GetGlobalTokenManager().TokenParamName != "" {
			tokenParamName = auth.GetGlobalTokenManager().TokenParamName
		}

		// 提取token参数，处理嵌套URL的情况
		var token string
		token = r.URL.Query().Get(tokenParamName)

		// 如果在常规查询参数中没有找到token，检查路径中是否包含嵌套URL
		if token == "" {
			// 检查路径是否包含嵌套URL格式（如 /http://... 或 /https://...）
			path := r.URL.Path
			if strings.HasPrefix(path, "/http://") || strings.HasPrefix(path, "/https://") {
				// 尝试解析整个路径作为URL
				fullPath := path
				if r.URL.RawQuery != "" {
					fullPath = path + "?" + r.URL.RawQuery
				}

				// 解析嵌套URL
				nestedURLStr := strings.TrimLeft(fullPath, "/")
				nestedURL, err := url.Parse(nestedURLStr)
				if err == nil {
					token = nestedURL.Query().Get(tokenParamName)
				}
			}
		}

		// // 获取客户端真实IP
		// clientIP := monitor.GetClientIP(r)

		// // 构造连接ID（IP+端口）
		// connID := clientIP + "_" + r.RemoteAddr

		// 验证全局token
		if !auth.GetGlobalTokenManager().ValidateToken(token, r.URL.Path, connID) {
			logger.LogPrintf("全局token验证失败: token=%s, path=%s, ip=%s", token, r.URL.Path, clientIP)
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		// 更新全局token活跃状态
		auth.GetGlobalTokenManager().KeepAlive(token, connID, clientIP, r.URL.Path)
		logger.LogPrintf("全局token验证成功: token=%s, path=%s, ip=%s", token, r.URL.Path, clientIP)
	}

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

	// 构建RTSP URL
	// 构建RTSP URL
	rtspURL := fmt.Sprintf("rtsp://%s%s", hostPort, streamPath)

	if r.URL.RawQuery != "" {
		if auth.GetGlobalTokenManager() != nil {
			// ✅ 全局认证启用，删除 token 参数
			query := r.URL.RawQuery
			parts := strings.Split(query, "&")
			newParts := []string{}
			for _, kv := range parts {
				if !strings.HasPrefix(kv, tokenParamName+"=") {
					newParts = append(newParts, kv)
				}
			}
			if len(newParts) > 0 {
				rtspURL += "?" + strings.Join(newParts, "&")
			}
		} else {
			// ✅ 全局认证未启用，保留原始 query
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

	transport := gortsplib.TransportTCP
	client := &gortsplib.Client{
		Transport: &transport,
	}
	// 先不关闭client，由hub决定是否需要关闭
	// defer client.Close()

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

	// 获取或创建 StreamHub
	hub := stream.GetOrCreateHubs(rtspURL)

	// 定义媒体变量
	var (
		videoMedia   *description.Media
		videoFormat  *format.H264
		mpegtsFormat *format.MPEGTS
		audioMedia   *description.Media
		audioFormat  *format.MPEG4Audio
	)

	// 检查hub中是否已经有RTSP客户端
	existingClient := hub.GetRtspClient()

	// 如果已经有RTSP客户端在运行，则复用它，否则创建新的连接
	if existingClient != nil {
		client = existingClient

		// 从hub中获取媒体信息
		storedVideoMedia, storedVideoFormat, storedAudioMedia, storedAudioFormat := hub.GetMediaInfo()

		// 如果hub中有存储的媒体信息，使用它们
		if storedVideoMedia != nil {
			videoMedia = storedVideoMedia
		}
		if storedVideoFormat != nil {
			// 根据类型判断是MPEGTS还是H264格式
			if f, ok := storedVideoFormat.(*format.MPEGTS); ok {
				mpegtsFormat = f
			} else if f, ok := storedVideoFormat.(*format.H264); ok {
				videoFormat = f
			}
		}
		if storedAudioMedia != nil {
			audioMedia = storedAudioMedia
		}
		if storedAudioFormat != nil {
			audioFormat = storedAudioFormat
		}

		// 检查媒体信息是否有效
		if mpegtsFormat == nil && videoFormat == nil {
			logger.LogPrintf("Warning: No valid media info in existing connection for %s", rtspURL)
		}
	} else {
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

		// 保存媒体信息到hub
		if mpegtsFormat != nil {
			hub.SetMediaInfo(videoMedia, mpegtsFormat)
		}
		// 如果是H264格式，也保存相关信息
		if videoFormat != nil {
			hub.SetMediaInfo(videoMedia, videoFormat)
		}
		// 保存音频信息
		if audioFormat != nil {
			hub.SetAudioMediaInfo(audioMedia, audioFormat)
		}
	}

	ctx := r.Context()
	// 转发前每次更新活跃时间
	updateActive := func() {
		monitor.ActiveClients.UpdateLastActive(connID, time.Now())
	}
	// 只处理MPEGTS格式，避免H264+AAC的空指针问题
	if mpegtsFormat != nil && videoMedia != nil {
		// 设置RTSP客户端
		hub.SetRtspClient(client)

		// 调用 HandleMpegtsStream，传入 hub
		if err := stream.HandleMpegtsStream(ctx, w, client, videoMedia, mpegtsFormat, r, rtspURL, hub, updateActive); err != nil {
			http.Error(w, "Stream error: "+err.Error(), 500)
		}
		return
	}

	// 对于H264+AAC格式，只在有完整信息时处理
	if videoFormat != nil && videoMedia != nil {
		// 设置RTSP客户端
		hub.SetRtspClient(client)

		if err := stream.HandleH264AacStream(ctx, w, client, videoMedia, videoFormat, audioMedia, audioFormat, r, rtspURL, hub, updateActive); err != nil {
			http.Error(w, "Stream error: "+err.Error(), 500)
		}
		return
	}

	// 如果没有支持的格式，返回错误
	http.Error(w, "No supported stream format found", 500)
}
