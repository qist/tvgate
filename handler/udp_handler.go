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
	// 全局token验证
	if auth.GetGlobalTokenManager() != nil {
		tokenParamName := "my_token" // 默认参数名
		token := r.URL.Query().Get(tokenParamName)

		// 获取客户端真实IP
		clientIP := monitor.GetClientIP(r)

		// 构造连接ID（IP+端口）
		connID := clientIP + "_" + r.RemoteAddr

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

	// URL 形如 /rtp/239.0.0.1:5000?iface=eth0,eth1
	addr := r.URL.Path[len(prefix):]
	if addr == "" || !strings.Contains(addr, ":") {
		http.Error(w, "Address must be ip:port", http.StatusBadRequest)
		return
	}

	// 覆盖配置的 iface
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

	hub, err := stream.GetOrCreateHub(addr, ifaces)
	if err != nil {
		http.Error(w, "Failed to listen UDP: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 确定连接类型 (从 prefix 取 "/udp/" 或 "/rtp/")
	connectionType := "UDP"
	if strings.HasPrefix(prefix, "/rtp/") {
		connectionType = "RTP"
	}
	// 注册活跃客户端
	clientIP := monitor.GetClientIP(r)
	connID := clientIP + "_" + addr + "_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	monitor.ActiveClients.Register(connID, &monitor.ClientConnection{
		IP:             clientIP,
		URL:            addr,
		UserAgent:      r.UserAgent(),
		ConnectionType: connectionType,
		ConnectedAt:    time.Now(),
		LastActive:     time.Now(),
	})
	defer monitor.ActiveClients.Unregister(connID,connectionType)

	// 定义更新活跃时间的回调
	updateActive := func() {
		monitor.ActiveClients.UpdateLastActive(connID, time.Now())
	}
	logger.LogRequestAndResponse(r, addr, &http.Response{StatusCode: http.StatusOK})
	hub.ServeHTTP(w, r, "application/octet-stream", updateActive)
}
