package monitor

import (
	"sync"
	"time"
)

// ClientConnection 表示一个客户端连接
type ClientConnection struct {
	IP             string
	URL            string
	UserAgent      string
	Referer        string
	ConnectionType string // RTSP/HTTP/UDP
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

func (m *ActiveConnectionsManager) Register(connID string, conn *ClientConnection) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.conns[connID] = conn
}

func (m *ActiveConnectionsManager) Unregister(connID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.conns, connID)
}

func (m *ActiveConnectionsManager) GetAll() []*ClientConnection {
	m.mu.RLock()
	defer m.mu.RUnlock()
	list := make([]*ClientConnection, 0, len(m.conns))
	for _, c := range m.conns {
		list = append(list, c)
	}
	return list
}

func (m *ActiveConnectionsManager) UpdateLastActive(connID string, t time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.conns[connID]; ok {
		c.LastActive = t
	}
}
