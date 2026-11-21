package config

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// --------------------------- 代理池解析 ---------------------------

type poolServerSpec struct {
	Scheme   string
	Host     string
	PortFrom int
	PortTo   int
	Username string
	Password string
}

var poolProtocols = map[string]bool{
	"":        true, // 由 pool.protocol 决定
	"auto":    true,
	"http":    true,
	"https":   true,
	"socks4":  true,
	"socks4a": true,
	"socks5":  true,
	"socks5h": true, // 兼容写法，等价 socks5
}

func normalizeResolve(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "remote":
		return "remote"
	case "local":
		return "local"
	default:
		return "remote"
	}
}

func normalizePoolProtocol(v string) string {
	p := strings.ToLower(strings.TrimSpace(v))
	if p == "" {
		return "auto"
	}
	if p == "socks5h" {
		return "socks5"
	}
	return p
}

// EffectiveProbeEnabled 返回定时探测开关，未配置时默认开启。
func (p *ProxyPoolConfig) EffectiveProbeEnabled() bool {
	return p != nil && (p.ProbeEnabled == nil || *p.ProbeEnabled)
}

// Validate 校验池配置。此方法不修改配置，允许字段留空（默认值由 SetDefaults 补齐）。
func (p *ProxyPoolConfig) Validate() error {
	if p == nil {
		return nil
	}
	proto := normalizePoolProtocol(p.Protocol)
	if !poolProtocols[proto] {
		return fmt.Errorf("pool.protocol 不支持: %q", p.Protocol)
	}
	if len(p.Servers) == 0 {
		return fmt.Errorf("pool.servers 不能为空")
	}
	maxPorts := p.MaxPorts
	if maxPorts <= 0 {
		maxPorts = 2048
	}
	total := 0
	for _, raw := range p.Servers {
		spec, err := parsePoolServer(raw)
		if err != nil {
			return fmt.Errorf("pool.servers %q: %w", raw, err)
		}
		if !poolProtocols[spec.Scheme] {
			return fmt.Errorf("pool.servers %q: 不支持的协议 %q", raw, spec.Scheme)
		}
		total += spec.PortTo - spec.PortFrom + 1
		if total > maxPorts {
			return fmt.Errorf("pool.servers 展开后端口数 %d 超过 max_ports=%d", total, maxPorts)
		}
	}
	if u := strings.TrimSpace(p.ProbeURL); u != "" {
		parsed, err := url.Parse(u)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "rtsp") {
			return fmt.Errorf("pool.probe_url 必须是 http/https/rtsp 地址: %q", p.ProbeURL)
		}
	}
	return nil
}

// BuildPoolNodes 把 pool.servers 展开成运行时候选节点。
// 返回的节点不写入配置文件，只用于内存中的探测和选路。
func (g *ProxyGroupConfig) BuildPoolNodes() ([]*ProxyConfig, error) {
	if g == nil || g.Pool == nil {
		return nil, nil
	}
	if err := g.Pool.Validate(); err != nil {
		return nil, err
	}

	poolProto := normalizePoolProtocol(g.Pool.Protocol)
	resolve := normalizeResolve(g.Resolve)
	maxPorts := g.Pool.MaxPorts
	if maxPorts <= 0 {
		maxPorts = 2048
	}

	nodes := make([]*ProxyConfig, 0, 64)
	seen := make(map[string]struct{})
	ports := 0

	for _, raw := range g.Pool.Servers {
		spec, err := parsePoolServer(raw)
		if err != nil {
			return nil, err
		}

		protos := []string{}
		if spec.Scheme != "" && spec.Scheme != "auto" {
			protos = append(protos, normalizePoolProtocol(spec.Scheme))
		} else if poolProto == "auto" {
			// 同一端口可能只开放其中一种协议，两种都作为候选后台探测。
			protos = append(protos, "socks5", "http")
		} else {
			protos = append(protos, poolProto)
		}

		for port := spec.PortFrom; port <= spec.PortTo; port++ {
			ports++
			if ports > maxPorts {
				return nil, fmt.Errorf("pool.servers 展开后端口数超过 max_ports=%d", maxPorts)
			}
			for _, proto := range protos {
				proto = normalizePoolProtocol(proto)
				if !poolProtocols[proto] || proto == "auto" || proto == "" {
					return nil, fmt.Errorf("pool.servers %q: 无法确定代理协议", raw)
				}
				key := fmt.Sprintf("%s|%s|%d|%s|%s", proto, spec.Host, port, spec.Username, spec.Password)
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				nodes = append(nodes, &ProxyConfig{
					Name:     fmt.Sprintf("pool-%s-%d-%s", poolNameHost(spec.Host), port, proto),
					Type:     proto,
					Server:   spec.Host,
					Port:     port,
					Username: spec.Username,
					Password: spec.Password,
					Resolve:  resolve,
				})
			}
		}
	}
	return nodes, nil
}

// PoolKey 生成池配置的稳定标识，用于热重载时判断是否需要重建探测状态。
// 内部会补齐默认值，避免「旧配置已 SetDefaults、新配置未 SetDefaults」被误判为变化。
func (g *ProxyGroupConfig) PoolKey() string {
	if g == nil || g.Pool == nil {
		return ""
	}
	p := *g.Pool
	p.SetDefaults()
	enabled := p.ProbeEnabled == nil || *p.ProbeEnabled
	return strings.Join(p.Servers, ",") + fmt.Sprintf("|%s|%t|%s|%s|%s|%d|%d|%d|%s|%d",
		normalizePoolProtocol(p.Protocol),
		enabled,
		p.ProbeInterval,
		p.ProbeURL,
		p.ProbeTimeout,
		p.ProbeRetries,
		p.ProbeConcurrency,
		p.TopN,
		p.FailCooldown,
		p.MaxPorts,
	) + "|" + normalizeResolve(g.Resolve)
}

func parsePoolServer(raw string) (poolServerSpec, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return poolServerSpec{}, fmt.Errorf("空服务器地址")
	}

	spec := poolServerSpec{}
	rest := raw
	if idx := strings.Index(raw, "://"); idx >= 0 {
		spec.Scheme = strings.ToLower(strings.TrimSpace(raw[:idx]))
		rest = raw[idx+3:]
	}
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		cred := rest[:at]
		rest = rest[at+1:]
		if colon := strings.Index(cred, ":"); colon >= 0 {
			spec.Username = cred[:colon]
			spec.Password = cred[colon+1:]
		} else {
			spec.Username = cred
		}
		if v, err := url.QueryUnescape(spec.Username); err == nil {
			spec.Username = v
		}
		if v, err := url.QueryUnescape(spec.Password); err == nil {
			spec.Password = v
		}
	}

	host, portSpec, err := splitHostPortRange(rest)
	if err != nil {
		return poolServerSpec{}, err
	}
	spec.Host = host
	from, to, err := parsePortRange(portSpec)
	if err != nil {
		return poolServerSpec{}, err
	}
	spec.PortFrom, spec.PortTo = from, to
	return spec, nil
}

func splitHostPortRange(raw string) (host, portSpec string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("缺少 host:port")
	}
	if strings.HasPrefix(raw, "[") {
		end := strings.Index(raw, "]")
		if end < 0 {
			return "", "", fmt.Errorf("IPv6 地址缺少 ]")
		}
		host = raw[1:end]
		remain := raw[end+1:]
		if !strings.HasPrefix(remain, ":") {
			return "", "", fmt.Errorf("缺少端口")
		}
		portSpec = remain[1:]
	} else {
		if strings.Count(raw, ":") != 1 {
			return "", "", fmt.Errorf("格式应为 host:port 或 host:start-end")
		}
		idx := strings.LastIndex(raw, ":")
		host = raw[:idx]
		portSpec = raw[idx+1:]
	}
	if host == "" || portSpec == "" {
		return "", "", fmt.Errorf("格式应为 host:port 或 host:start-end")
	}
	return host, portSpec, nil
}

func parsePortRange(s string) (int, int, error) {
	parts := strings.SplitN(strings.TrimSpace(s), "-", 2)
	from, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || from <= 0 || from > 65535 {
		return 0, 0, fmt.Errorf("端口无效: %q", s)
	}
	to := from
	if len(parts) == 2 {
		to, err = strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || to <= 0 || to > 65535 {
			return 0, 0, fmt.Errorf("端口范围无效: %q", s)
		}
	}
	if to < from {
		return 0, 0, fmt.Errorf("端口范围结束值小于起始值: %q", s)
	}
	return from, to, nil
}

func poolNameHost(host string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, host)
}

// --------------------------- 运行时可用节点 ---------------------------

// activeProxyMu 保护所有代理组的 activeProxies。池探测更新频率低，使用包级锁即可，
// 避免把 mutex 放进可被复制的 ProxyGroupConfig。
var activeProxyMu sync.RWMutex

// SetPoolNodes 设置 pool 探测后的可用节点；静态 proxies 始终保留在列表前面。
func (g *ProxyGroupConfig) SetPoolNodes(nodes []*ProxyConfig) {
	if g == nil {
		return
	}
	combined := make([]*ProxyConfig, 0, len(g.Proxies)+len(nodes))
	combined = append(combined, g.Proxies...)
	combined = append(combined, nodes...)
	activeProxyMu.Lock()
	g.poolNodes = nodes
	g.activeProxies = combined
	activeProxyMu.Unlock()
}

// InheritPoolRuntime 把旧组的池节点和测速状态复制到新组。
// 只在 PoolKey 完全一致时调用，避免配置变化后误用旧节点。
func (g *ProxyGroupConfig) InheritPoolRuntime(old *ProxyGroupConfig) {
	if g == nil || old == nil || g.PoolKey() == "" || g.PoolKey() != old.PoolKey() {
		return
	}
	activeProxyMu.RLock()
	nodes := old.poolNodes
	activeProxyMu.RUnlock()
	if len(nodes) == 0 {
		return
	}
	g.SetPoolNodes(nodes)
	if g.Stats == nil {
		g.Stats = &GroupStats{ProxyStats: make(map[string]*ProxyStats)}
	}
	if old.Stats != nil {
		old.Stats.RLock()
		for _, n := range nodes {
			if st, ok := old.Stats.ProxyStats[n.Name]; ok {
				g.Stats.ProxyStats[n.Name] = st
			}
		}
		old.Stats.RUnlock()
	}
}

// ActiveProxies 返回当前实际参与选路的节点（静态 + 池）。
// 返回的切片只读，调用方不得修改。
func (g *ProxyGroupConfig) ActiveProxies() []*ProxyConfig {
	if g == nil {
		return nil
	}
	activeProxyMu.RLock()
	active := g.activeProxies
	activeProxyMu.RUnlock()
	if active != nil {
		return active
	}
	return g.Proxies
}

// PoolNodes 返回当前池探测得到的运行时节点（仅内存，不写回配置）。
// 供 Web 后台展示成功端口，调用方不得修改返回的节点。
func (g *ProxyGroupConfig) PoolNodes() []*ProxyConfig {
	if g == nil {
		return nil
	}
	activeProxyMu.RLock()
	defer activeProxyMu.RUnlock()
	if len(g.poolNodes) == 0 {
		return nil
	}
	out := make([]*ProxyConfig, len(g.poolNodes))
	copy(out, g.poolNodes)
	return out
}

// ClearPoolNodes 清空运行时池节点，回落到静态 proxies。
func (g *ProxyGroupConfig) ClearPoolNodes() {
	if g == nil {
		return
	}
	activeProxyMu.Lock()
	g.activeProxies = nil
	g.poolNodes = nil
	activeProxyMu.Unlock()
}

// PoolDefaults 补全池配置默认值。
func (p *ProxyPoolConfig) SetDefaults() {
	if p == nil {
		return
	}
	p.Protocol = normalizePoolProtocol(p.Protocol)
	if p.ProbeEnabled == nil {
		v := true
		p.ProbeEnabled = &v
	}
	if p.ProbeInterval <= 0 {
		p.ProbeInterval = 60 * time.Second
	}
	if p.ProbeInterval < 10*time.Second {
		p.ProbeInterval = 10 * time.Second
	}
	if p.ProbeTimeout <= 0 {
		p.ProbeTimeout = 2 * time.Second
	}
	if p.ProbeRetries < 0 {
		p.ProbeRetries = 0
	}
	if p.ProbeRetries > 5 {
		p.ProbeRetries = 5
	}
	if p.ProbeConcurrency <= 0 {
		p.ProbeConcurrency = 16
	}
	if p.ProbeConcurrency > 64 {
		p.ProbeConcurrency = 64
	}
	if p.TopN <= 0 {
		p.TopN = 30
	}
	if p.TopN > 200 {
		p.TopN = 200
	}
	if p.FailCooldown <= 0 {
		p.FailCooldown = 60 * time.Second
	}
	if p.MaxPorts <= 0 {
		p.MaxPorts = 2048
	}
}
