package handler

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
	"github.com/qist/tvgate/lb"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/proxy"
	"github.com/qist/tvgate/rules"
	"github.com/qist/tvgate/stream"
)

// 读超时包装器，给响应体读加超时控制，避免代理响应体卡死
type timeoutReadCloser struct {
	io.ReadCloser
	timeout time.Duration
}

func (t *timeoutReadCloser) Read(p []byte) (int, error) {
	type readResult struct {
		n   int
		err error
	}
	resultChan := make(chan readResult, 1)

	go func() {
		n, err := t.ReadCloser.Read(p)
		resultChan <- readResult{n, err}
	}()

	select {
	case res := <-resultChan:
		return res.n, res.err
	case <-time.After(t.timeout):
		return 0, fmt.Errorf("读取响应体超时，强制关闭连接")
	}
}

// handler 函数，整合读超时处理
func Handler(client *http.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Server", "TVGate")

			msg := fmt.Sprintf("TVGate running\nVersion: %s\nProtocols: HTTP/1.1, HTTP/2, HTTP/3\n", config.Version)
			_, _ = w.Write([]byte(msg))
			return
		}

		if r.URL.Path == "/favicon.ico" {
			w.Header().Set("Content-Type", "image/x-icon")
			w.Header().Set("server", "TVGate")
			w.Write(config.FaviconFile)
			return
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/udp/"):
			UdpRtpHandler(w, r, "/udp/")
			return
		case strings.HasPrefix(r.URL.Path, "/rtp/"):
			UdpRtpHandler(w, r, "/rtp/")
			return
		case strings.HasPrefix(r.URL.Path, "/rtsp/"):
			RtspToHTTPHandler(w, r)
			return
		}

		ctx, cancel := context.WithCancel(context.Background()) // 可加超时限制
		defer cancel()

		// 安全读取请求体（非 GET/HEAD 且有 Body）
		var bodyBytes []byte
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Body != nil {
			var err error
			bodyBytes, err = io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "读取请求体失败", http.StatusInternalServerError)
				return
			}
		}
		r.Body.Close()

		// 计算目标地址
		targetPath := stream.GetTargetPath(r)
		targetURL := stream.GetTargetURL(r, targetPath)

		parsedURL, err := url.Parse(targetURL)
		if err != nil {
			http.Error(w, "无效的目标 URL", http.StatusBadRequest)
			return
		}

		// 构造直连请求
		var originBody io.ReadCloser
		if len(bodyBytes) > 0 {
			originBody = io.NopCloser(bytes.NewReader(bodyBytes))
		}
		originReq, err := http.NewRequest(r.Method, targetURL, originBody)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		originReq = originReq.WithContext(ctx)
		stream.CopyHeadersExceptSensitive(originReq.Header, r.Header, r.ProtoMajor)

		// 选择代理组
		hostname := parsedURL.Hostname()
		originalHost := rules.ExtractOriginalDomain(r.URL.Path)
		if originalHost == "" {
			originalHost = r.URL.Query().Get("original_host")
		}

		if pg := rules.ChooseProxyGroup(hostname, originalHost); pg != nil {
			maxRetries := pg.MaxRetries
			if maxRetries <= 0 {
				maxRetries = 1
			}
			retryDelay := pg.RetryDelay
			readTimeout := 10 * time.Second // 响应体读超时

			for attempt := 0; attempt <= maxRetries; attempt++ {
				forceTest := attempt > 0

				// 异步选择代理
				proxyRes := make(chan *config.ProxyConfig, 1)
				go func() {
					proxyRes <- lb.SelectProxy(pg, targetURL, forceTest)
				}()

				var selectedProxy *config.ProxyConfig
				select {
				case selectedProxy = <-proxyRes:
					if selectedProxy != nil {
						logger.LogPrintf("异步选择代理成功: %s", selectedProxy.Name)
					} else {
						logger.LogPrintf("异步选择代理返回 nil（第 %d 次尝试）", attempt+1)
					}
				case <-time.After(config.DefaultDialTimeout):
					logger.LogPrintf("异步选择代理未完成，继续直连或下一次尝试（第 %d 次）", attempt+1)
					selectedProxy = nil
				}

				if selectedProxy == nil {
					// 如果没有可用代理，尝试下一次或直连
					if attempt == maxRetries {
						logger.LogPrintf("❌ 未找到可用代理，使用直连")
						break
					}
					time.Sleep(retryDelay)
					continue
				}

				proxyClient, err := proxy.CreateProxyClient(ctx, &config.Cfg, *selectedProxy, pg.IPv6)
				if err != nil {
					markProxyResult(pg, selectedProxy, false)
					continue
				}

				// 构造代理请求
				var proxyBody io.ReadCloser
				if len(bodyBytes) > 0 {
					proxyBody = io.NopCloser(bytes.NewReader(bodyBytes))
				}
				reqCopy, err := http.NewRequest(r.Method, targetURL, proxyBody)
				if err != nil {
					markProxyResult(pg, selectedProxy, false)
					continue
				}
				reqCopy = reqCopy.WithContext(ctx)
				stream.CopyHeadersExceptSensitive(reqCopy.Header, r.Header, r.ProtoMajor)

				// 发起代理请求
				proxyResp, err := proxyClient.Do(reqCopy)
				if err != nil {
					logger.LogPrintf("⚠️ 代理请求网络错误（第 %d 次）：%v", attempt+1, err)
					markProxyResult(pg, selectedProxy, false)
					if attempt == maxRetries {
						http.Error(w, "代理请求失败："+err.Error(), http.StatusBadGateway)
						return
					}
					time.Sleep(retryDelay)
					continue
				}

				if proxyResp == nil {
					logger.LogPrintf("⚠️ 代理请求无响应（第 %d 次）", attempt+1)
					markProxyResult(pg, selectedProxy, false)
					if attempt == maxRetries {
						http.Error(w, "代理无响应", http.StatusBadGateway)
						return
					}
					time.Sleep(retryDelay)
					continue
				}

				proxyResp.Body = &timeoutReadCloser{
					ReadCloser: proxyResp.Body,
					timeout:    readTimeout,
				}

				if proxyResp.StatusCode >= 500 {
					logger.LogPrintf("⚠️ 代理服务器错误状态码 %d（第 %d 次）", proxyResp.StatusCode, attempt+1)
					proxyResp.Body.Close()
					markProxyResult(pg, selectedProxy, false)
					if attempt == maxRetries {
						http.Error(w, fmt.Sprintf("代理服务器错误状态码: %d", proxyResp.StatusCode), http.StatusBadGateway)
						return
					}
					time.Sleep(retryDelay)
					continue
				}

				// 成功处理响应
				markProxyResult(pg, selectedProxy, true)
				stream.HandleProxyResponse(ctx, w, r, targetURL, proxyResp)
				return
			}
		}
		// fallback: 直连请求
		clientResp, err := client.Do(originReq)
		if err != nil {
			http.Error(w, "直连请求失败："+err.Error(), http.StatusBadGateway)
			return
		}
		if clientResp == nil {
			http.Error(w, "直连无响应", http.StatusBadGateway)
			return
		}
		defer clientResp.Body.Close()
		if clientResp.StatusCode >= 500 {
			http.Error(w, fmt.Sprintf("服务器返回错误状态码: %d", clientResp.StatusCode), http.StatusBadGateway)
			return
		}
		stream.HandleProxyResponse(ctx, w, r, targetURL, clientResp)
	}
}

func markProxyResult(group *config.ProxyGroupConfig, proxy *config.ProxyConfig, alive bool) {
	group.Stats.Lock()
	defer group.Stats.Unlock()

	stats, ok := group.Stats.ProxyStats[proxy.Name]
	if !ok {
		stats = &config.ProxyStats{}
		group.Stats.ProxyStats[proxy.Name] = stats
	}

	stats.Alive = alive
	// stats.LastCheck = time.Now()
	// 不一定每次都更新 TestURL 和 ResponseTime，可视需要添加
}
