package handler

import (
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/monitor"
	"github.com/qist/tvgate/stream"
	"strconv"
	"time"
	"net/http"
	"strings"
)

func UdpRtpHandler(w http.ResponseWriter, r *http.Request, prefix string) {
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
	// 注册活跃客户端
	clientIP := monitor.GetClientIP(r)
	connID := clientIP + "_" + addr + "_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	monitor.ActiveClients.Register(connID, &monitor.ClientConnection{
		IP:             clientIP,
		URL:            addr,
		UserAgent:      r.UserAgent(),
		ConnectionType: "UDP",
		ConnectedAt:    time.Now(),
		LastActive:     time.Now(),
	})
	defer monitor.ActiveClients.Unregister(connID)

	// 定义更新活跃时间的回调
	updateActive := func() {
		monitor.ActiveClients.UpdateLastActive(connID, time.Now())
	}
	logger.LogRequestAndResponse(r, addr, &http.Response{StatusCode: http.StatusOK})
	hub.ServeHTTP(w, r, "application/octet-stream",updateActive)
}
