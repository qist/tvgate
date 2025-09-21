package monitor

import (
	"sync"
	"time"
)

// ClientConnection 表示一个客户端连接
type ClientConnection struct {
	ID             string
	IP             string
	URL            string
	UserAgent      string
	Referer        string
	ConnectionType string // RTSP/HTTP/UDP/HTTPS
	IsMobile       bool
	ConnectedAt    time.Time
	LastActive     time.Time
}

// ActiveConnectionsManager 管理活跃客户端
type ActiveConnectionsManager struct {
	conns map[string]*ClientConnection
	mu    sync.RWMutex
}

// 全局活跃客户端管理器
var ActiveClients = &ActiveConnectionsManager{
	conns: make(map[string]*ClientConnection),
}

// Register 注册客户端连接
func (m *ActiveConnectionsManager) Register(connID string, conn *ClientConnection) {
	m.mu.Lock()
	defer m.mu.Unlock()

	conn.ID = connID
	if existing, ok := m.conns[connID]; ok {
		// 已存在，更新
		existing.URL = conn.URL
		existing.UserAgent = conn.UserAgent
		existing.Referer = conn.Referer
		existing.ConnectionType = conn.ConnectionType
		existing.IsMobile = conn.IsMobile
		existing.LastActive = time.Now()
	} else {
		// 新连接
		conn.ConnectedAt = time.Now()
		conn.LastActive = conn.ConnectedAt
		m.conns[connID] = conn
	}
}

// Unregister 注销客户端连接
func (m *ActiveConnectionsManager) Unregister(connID string, connType string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if conn, ok := m.conns[connID]; ok {
		if connType == "RTSP" || connType == "UDP" {
			// RTSP/UDP → 立即删除
			delete(m.conns, connID)
		} else {
			// HTTP/HTTPS → 更新最后活跃，等待 Cleaner 清理
			conn.LastActive = time.Now()
		}
	}
}

// GetAll 获取所有活跃客户端连接
func (m *ActiveConnectionsManager) GetAll() []*ClientConnection {
	m.mu.RLock()
	defer m.mu.RUnlock()

	list := make([]*ClientConnection, 0, len(m.conns))
	for _, c := range m.conns {
		list = append(list, c)
	}
	return list
}

// GetConnectionsByIP 获取指定IP的所有连接
func (m *ActiveConnectionsManager) GetConnectionsByIP(ip string) []*ClientConnection {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var list []*ClientConnection
	for _, c := range m.conns {
		if c.IP == ip {
			list = append(list, c)
		}
	}
	return list
}

// UpdateLastActive 更新客户端最后活跃时间
func (m *ActiveConnectionsManager) UpdateLastActive(connID string, t time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if c, ok := m.conns[connID]; ok {
		c.LastActive = t
	}
}

// CleanInactiveConnections 清理不活跃连接
func (m *ActiveConnectionsManager) CleanInactiveConnections(timeout time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for id, conn := range m.conns {
		if now.Sub(conn.LastActive) > timeout {
			delete(m.conns, id)
		}
	}
}

// StartCleaner 启动定时清理器
func (m *ActiveConnectionsManager) StartCleaner(interval time.Duration, timeout time.Duration, stopChan chan struct{}) {
	ticker := time.NewTicker(interval)
	go func() {
		for {
			select {
			case <-ticker.C:
			m.CleanInactiveConnections(timeout)
			case <-stopChan:
				ticker.Stop()
				return
			}
		}
	}()
}

// GetConnectionByID 根据 ConnID 获取单个客户端连接
func (m *ActiveConnectionsManager) GetConnectionByID(connID string) *ClientConnection {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if c, ok := m.conns[connID]; ok {
		return c
	}
	return nil
}
