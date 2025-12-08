package handler

import (
	"github.com/qist/tvgate/auth"
	"github.com/qist/tvgate/config"

	// "github.com/qist/tvgate/logger"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/qist/tvgate/monitor"
	"github.com/qist/tvgate/stream"
)

func UdpRtpHandler(w http.ResponseWriter, r *http.Request, prefix string) {
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

	// 解析 UDP 地址
	addr := r.URL.Path[len(prefix):]
	if addr == "" || !strings.Contains(addr, ":") {
		http.Error(w, "Address must be ip:port", http.StatusBadRequest)
		return
	}

	// 获取指定网卡
	var ifaces []string
	if s := r.URL.Query().Get("iface"); s != "" {
		for _, n := range strings.Split(s, ",") {
			n = strings.TrimSpace(n)
			if n != "" {
				ifaces = append(ifaces, n)
			}
		}
	} else {
		config.CfgMu.RLock()
		ifaces = append(ifaces, config.Cfg.Server.MulticastIfaces...)
		config.CfgMu.RUnlock()
	}

	// 检查是否启用FCC功能
	fccParam := r.URL.Query().Get("fcc")
	if fccParam != "" {
		// 使用FCC功能处理
		handleFCCRequest(w, r, addr, fccParam, ifaces, connID)
		return
	}

	// 使用 MultiChannelHub 获取或创建 Hub
	hub, err := stream.GlobalMultiChannelHub.GetOrCreateHub(addr, ifaces)
	if err != nil {

		http.Error(w, "Failed to listen UDP: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 注册客户端活跃信息
	connectionType := "UDP"
	if strings.HasPrefix(prefix, "/rtp/") {
		connectionType = "RTP"
	}
	monitor.ActiveClients.Register(connID, &monitor.ClientConnection{
		IP:             clientIP,
		URL:            addr,
		UserAgent:      r.UserAgent(),
		ConnectionType: connectionType,
		ConnectedAt:    time.Now(),
		LastActive:     time.Now(),
	})
	defer monitor.ActiveClients.Unregister(connID, connectionType)

	updateActive := func() {
		monitor.ActiveClients.UpdateLastActive(connID, time.Now())
	}

	hub.ServeHTTP(w, r, "video/mpeg", updateActive)
}

// handleFCCRequest 处理FCC请求
func handleFCCRequest(w http.ResponseWriter, r *http.Request, addr, fccParam string, ifaces []string, connID string) {
	// 获取或创建Hub
	hub, err := stream.GlobalMultiChannelHub.GetOrCreateHub(addr, ifaces)
	if err != nil {
		http.Error(w, "Failed to listen UDP: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 尝试把 fccParam 解析为毫秒数（回放过去多少毫秒的缓存）
	ms, err := strconv.Atoi(fccParam)
	if err != nil {
		// 如果不是数字，可能是单播 TCP 源（host:port），尝试以 TCP 单播方式回放
		// 优先支持 HTTP(s) URL 作为 FCC 源
		if strings.HasPrefix(fccParam, "http://") || strings.HasPrefix(fccParam, "https://") {
			// 设置响应头（与 ServeHTTP 保持一致）
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("ContentFeatures.DLNA.ORG", "DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=01700000000000000000000000000000")
			w.Header().Set("TransferMode.DLNA.ORG", "Streaming")
			w.Header().Set("Content-Type", "video/mpeg")

			// 注册客户端活跃信息（FCC-HTTP）
			clientIP := monitor.GetClientIP(r)
			connectionType := "UDP-FCC-HTTP"
			monitor.ActiveClients.Register(connID, &monitor.ClientConnection{
				IP:             clientIP,
				URL:            addr,
				UserAgent:      r.UserAgent(),
				ConnectionType: connectionType,
				ConnectedAt:    time.Now(),
				LastActive:     time.Now(),
			})
			defer monitor.ActiveClients.Unregister(connID, connectionType)

			updateActive := func() {
				monitor.ActiveClients.UpdateLastActive(connID, time.Now())
			}

			// 发起 HTTP GET，并以请求的 Context 关联，支持取消
			client := &http.Client{Timeout: 0}
			req, err := http.NewRequestWithContext(r.Context(), "GET", fccParam, nil)
			if err != nil {
				hub.ServeHTTP(w, r, "video/mpeg", updateActive)
				return
			}
			resp, err := client.Do(req)
			if err != nil {
				hub.ServeHTTP(w, r, "video/mpeg", updateActive)
				return
			}
			defer resp.Body.Close()

			// 读取响应体并写回客户端
			flusher, _ := w.(http.Flusher)
			buf := make([]byte, 32*1024)
			for {
				select {
				case <-r.Context().Done():
					return
				default:
				}
				n, rerr := resp.Body.Read(buf)
				if n > 0 {
					_, werr := w.Write(buf[:n])
					if werr != nil {
						return
					}
					if flusher != nil {
						flusher.Flush()
					}
				}
				if rerr != nil {
					if rerr == io.EOF {
						// 正常结束
						break
					}
					// 出错，结束回放并切换到实时流
					break
				}
			}

			// 切换到实时流
			hub.ServeHTTP(w, r, "video/mpeg", updateActive)
			return
		}

		if strings.Contains(fccParam, ":") {
			// 设置响应头（与 ServeHTTP 保持一致）
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("ContentFeatures.DLNA.ORG", "DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=01700000000000000000000000000000")
			w.Header().Set("TransferMode.DLNA.ORG", "Streaming")
			w.Header().Set("Content-Type", "video/mpeg")

			// 注册客户端活跃信息（FCC-TCP）
			clientIP := monitor.GetClientIP(r)
			connectionType := "UDP-FCC-TCP"
			monitor.ActiveClients.Register(connID, &monitor.ClientConnection{
				IP:             clientIP,
				URL:            addr,
				UserAgent:      r.UserAgent(),
				ConnectionType: connectionType,
				ConnectedAt:    time.Now(),
				LastActive:     time.Now(),
			})
			defer monitor.ActiveClients.Unregister(connID, connectionType)

			updateActive := func() {
				monitor.ActiveClients.UpdateLastActive(connID, time.Now())
			}

			// 尝试建立到 fcc TCP 源的连接
			tcpConn, err := net.DialTimeout("tcp", fccParam, 5*time.Second)
			if err != nil {
				// 无法连接到 TCP 源，回退到实时流
				hub.ServeHTTP(w, r, "video/mpeg", updateActive)
				return
			}
			defer tcpConn.Close()

			flusher, _ := w.(http.Flusher)

			// 从 TCP 源读取并回放到客户端，直到源结束或客户端断开
			buf := make([]byte, 32*1024)
			for {
				select {
				case <-r.Context().Done():
					return
				default:
				}
				tcpConn.SetReadDeadline(time.Now().Add(30 * time.Second))
				n, rerr := tcpConn.Read(buf)
				if n > 0 {
					_, werr := w.Write(buf[:n])
					if werr != nil {
						return
					}
					if flusher != nil {
						flusher.Flush()
					}
				}
				if rerr != nil {
					// 读取结束或出错，停止回放并切换到实时流
					break
				}
			}

			// 切换到实时流
			hub.ServeHTTP(w, r, "video/mpeg", updateActive)
			return
		}

		// 其他非数字字符串，退回为普通实时流
		clientIP := monitor.GetClientIP(r)
		connectionType := "UDP"
		monitor.ActiveClients.Register(connID, &monitor.ClientConnection{
			IP:             clientIP,
			URL:            addr,
			UserAgent:      r.UserAgent(),
			ConnectionType: connectionType,
			ConnectedAt:    time.Now(),
			LastActive:     time.Now(),
		})
		defer monitor.ActiveClients.Unregister(connID, connectionType)

		updateActive := func() {
			monitor.ActiveClients.UpdateLastActive(connID, time.Now())
		}

		hub.ServeHTTP(w, r, "video/mpeg", updateActive)
		return
	}

	// 设置响应头（与 ServeHTTP 保持一致）
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("ContentFeatures.DLNA.ORG", "DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=01700000000000000000000000000000")
	w.Header().Set("TransferMode.DLNA.ORG", "Streaming")
	w.Header().Set("Content-Type", "video/mpeg")

	// 注册客户端活跃信息（FCC）
	clientIP := monitor.GetClientIP(r)
	connectionType := "UDP-FCC"
	monitor.ActiveClients.Register(connID, &monitor.ClientConnection{
		IP:             clientIP,
		URL:            addr,
		UserAgent:      r.UserAgent(),
		ConnectionType: connectionType,
		ConnectedAt:    time.Now(),
		LastActive:     time.Now(),
	})
	defer monitor.ActiveClients.Unregister(connID, connectionType)

	updateActive := func() {
		monitor.ActiveClients.UpdateLastActive(connID, time.Now())
	}

	// 计算回放起始时间（过去 ms 毫秒）
	since := time.Now().Add(-time.Duration(ms) * time.Millisecond)

	// 尝试从 Hub 的 FCC 缓冲区获取数据并回放
	if hub != nil && hub.FCCManager != nil {
		if buffer, ok := hub.FCCManager.GetBuffer(hub.StreamID); ok && buffer != nil {
			flusher, _ := w.(http.Flusher)
			packets := buffer.GetPacketsSince(since)
			for _, p := range packets {
				select {
				case <-r.Context().Done():
					return
				default:
				}
				if len(p.Data) == 0 {
					continue
				}
				_, err := w.Write(p.Data)
				if err != nil {
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
	}

	// 回放完成后，切换到实时流
	hub.ServeHTTP(w, r, "video/mpeg", updateActive)
}
