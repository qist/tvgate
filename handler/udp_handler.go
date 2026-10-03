package handler

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/qist/tvgate/auth"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/monitor"
	"github.com/qist/tvgate/stream"
)

func UdpRtpHandler(w http.ResponseWriter, r *http.Request, prefix string) {
	clientIP := monitor.GetClientIP(r)
	connID := clientIP + "_" + strconv.FormatInt(time.Now().UnixNano(), 10)

	// 全局 token 验证
	var tokenParam, token string
	if gt := auth.GetGlobalTokenManager(); gt != nil {
		tokenParam = "my_token"
		if gt.TokenParamName != "" {
			tokenParam = gt.TokenParamName
		}
		token = r.URL.Query().Get(tokenParam)

		// 验证 token
		if !gt.ValidateToken(token, r.URL.Path, connID) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		// keep-alive
		gt.KeepAlive(token, connID, clientIP, r.URL.Path)

		// 删除 token 参数，用于拼接 URL
		query := r.URL.Query()
		query.Del(tokenParam)
		r.URL.RawQuery = query.Encode()
	}

	// 解析 UDP 地址并去除前后空格、重复斜杠
	addr := strings.TrimSpace(r.URL.Path[len(prefix):])
	addr = strings.TrimPrefix(addr, "/")
	addr = strings.ReplaceAll(addr, "//", "/")
	addr = strings.TrimSuffix(addr, "/") // 去掉末尾多余的 /

	if addr == "" || !strings.Contains(addr, ":") {
		http.Error(w, "Address must be ip:port", http.StatusBadRequest)
		return
	}

	// 验证端口是否合法
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		http.Error(w, "Invalid address format", http.StatusBadRequest)
		return
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		http.Error(w, "Invalid port number", http.StatusBadRequest)
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
		ifaces = append(ifaces, config.Cfg.Multicast.MulticastIfaces...)
		config.CfgMu.RUnlock()
	}

	// 使用 MultiChannelHub 获取或创建 Hub
	hub, err := stream.GlobalMultiChannelHub.GetOrCreateHub(addr, ifaces)
	if err != nil {
		http.Error(w, "Failed to listen UDP: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 处理 FCC 相关参数
	fccParam := r.URL.Query().Get("fcc")
	operatorParam := r.URL.Query().Get("operator")

	// 如果提供了 fcc 参数或 operator 参数，则尝试启用 FCC
	if fccParam != "" || operatorParam != "" {
		// 如果 fccParam 为 "false" 或 "0"，则显式禁用
		if fccParam == "false" || fccParam == "0" {
			hub.EnableFCC(false)
		} else {
			hub.EnableFCC(true)
			if operatorParam != "" {
				hub.SetFccType(operatorParam)
			}

			// 尝试解析地址
			if fccParam != "" {
				if _, err := net.ResolveUDPAddr("udp", fccParam); err == nil {
					if err := hub.SetFccServerAddr(fccParam); err != nil {
						logger.LogPrintf("设置FCC服务器地址失败: %v", err)
					}
				} else if fccParam == "true" || fccParam == "1" {
					logger.LogPrintf("FCC已通过布尔值启用")
				} else {
					logger.LogPrintf("解析FCC服务器地址失败: %v", err)
				}
			}
		}
	}

	// 注册客户端活跃信息
	connectionType := "UDP"
	if strings.HasPrefix(prefix, "/rtp/") {
		connectionType = "RTP"
	}

	// 拼接完整 URL
	fullURL := addr
	if rawQuery := r.URL.RawQuery; rawQuery != "" {
		fullURL += "?" + rawQuery
	}

	monitor.ActiveClients.Register(connID, &monitor.ClientConnection{
		IP:             clientIP,
		URL:            fullURL,
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
