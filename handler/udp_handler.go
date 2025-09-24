package handler

import (
	"github.com/qist/tvgate/auth"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/monitor"
	"github.com/qist/tvgate/stream"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func UdpRtpHandler(w http.ResponseWriter, r *http.Request, prefix string) {
	clientIP := monitor.GetClientIP(r)
	connID := clientIP + "_" + strconv.FormatInt(time.Now().UnixNano(), 10)

	logger.LogPrintf("DEBUG: UDP请求开始处理 - Path: %s, ClientIP: %s, ConnID: %s", r.URL.Path, clientIP, connID)

	// 全局 token 验证
	if auth.GetGlobalTokenManager() != nil {
		tokenParam := "my_token"
		token := r.URL.Query().Get(tokenParam)

		if !auth.GetGlobalTokenManager().ValidateToken(token, r.URL.Path, connID) {
			logger.LogPrintf("全局 token 验证失败: token=%s, path=%s, ip=%s", token, r.URL.Path, clientIP)
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		auth.GetGlobalTokenManager().KeepAlive(token, connID, clientIP, r.URL.Path)
		logger.LogPrintf("DEBUG: 全局 token 验证成功: token=%s, path=%s, ip=%s", token, r.URL.Path, clientIP)
	}

	// 解析 UDP 地址
	addr := r.URL.Path[len(prefix):]
	if addr == "" || !strings.Contains(addr, ":") {
		logger.LogPrintf("DEBUG: 地址格式错误 - Addr: %s", addr)
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

	logger.LogPrintf("DEBUG: 获取或创建 Hub - Addr: %s, Ifaces: %v", addr, ifaces)

	// 使用 MultiChannelHub 获取或创建 Hub
	hub, err := stream.GlobalMultiChannelHub.GetOrCreateHub(addr, ifaces)
	if err != nil {
		logger.LogPrintf("DEBUG: 创建 Hub 失败 - Addr: %s, Error: %v", addr, err)
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

	logger.LogPrintf("DEBUG: 开始 HTTP 流服务 - Addr: %s", addr)
	hub.ServeHTTP(w, r, "application/octet-stream", updateActive)
	logger.LogPrintf("DEBUG: HTTP 流服务结束 - Addr: %s", addr)
}
