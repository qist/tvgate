package handler

import (
	"github.com/qist/tvgate/auth"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/monitor"
	"github.com/qist/tvgate/stream"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
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

	// 使用 MultiChannelHub 获取或创建 Hub
	hub, err := stream.GlobalMultiChannelHub.GetOrCreateHub(addr, ifaces)
	if err != nil {

		http.Error(w, "Failed to listen UDP: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 处理FCC相关参数
	fccParam := r.URL.Query().Get("fcc")
	operatorParam := r.URL.Query().Get("operator")

	if fccParam != "" {
		// 启用FCC功能
		hub.EnableFCC(true)

		// 设置FCC类型
		if operatorParam != "" {
			hub.SetFccType(operatorParam)
		}

		// 解析并设置FCC服务器地址
		if _, err := net.ResolveUDPAddr("udp", fccParam); err == nil {
			if err := hub.SetFccServerAddr(fccParam); err != nil {
				logger.LogPrintf("设置FCC服务器地址失败: %v", err)
			}
		} else {
			logger.LogPrintf("解析FCC服务器地址失败: %v", err)
		}
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
