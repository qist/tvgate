package config

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"sync"
	"time"
)

var (
	ConfigFilePath *string
	VersionFlag    *bool
	ServerCtx      context.Context
	Cancel         context.CancelFunc
	LogConfigMutex sync.Mutex
	Cfg            Config
	CfgMu          sync.RWMutex
	StartTime      time.Time // 程序启动时间
	initOnce       sync.Once
)

func init() {
	ConfigFilePath = flag.String("config", "config.yaml", "YAML配置文件路径")
	VersionFlag = flag.Bool("version", false, "显示程序版本")
	ServerCtx, Cancel = context.WithCancel(context.Background())
	StartTime = time.Now()
}

// Config 主配置结构
type Config struct {
	Server struct {
		Port            int      `yaml:"port"`
		CertFile        string   `yaml:"certfile"`
		KeyFile         string   `yaml:"keyfile"`
		SSLProtocols    string   `yaml:"ssl_protocols"`
		SSLCiphers      string   `yaml:"ssl_ciphers"`
		SSLECDHCurve    string   `yaml:"ssl_ecdh_curve"`
		MulticastIfaces []string `yaml:"multicast_ifaces"`
	} `yaml:"server"`

	Log struct {
		Enabled    bool   `yaml:"enabled"`
		File       string `yaml:"file"`
		MaxSizeMB  int    `yaml:"maxsize"`
		MaxBackups int    `yaml:"maxbackups"`
		MaxAgeDays int    `yaml:"maxage"`
		Compress   bool   `yaml:"compress"`
	} `yaml:"log"`

	HTTP struct {
		Timeout               time.Duration `yaml:"timeout"`                 // 整体请求超时 (0 = 不限制)
		ConnectTimeout        time.Duration `yaml:"connect_timeout"`         // TCP连接超时
		KeepAlive             time.Duration `yaml:"keepalive"`               // TCP保活
		ResponseHeaderTimeout time.Duration `yaml:"response_header_timeout"` // 等响应头超时
		IdleConnTimeout       time.Duration `yaml:"idle_conn_timeout"`       // 空闲连接超时
		TLSHandshakeTimeout   time.Duration `yaml:"tls_handshake_timeout"`   // TLS握手超时
		ExpectContinueTimeout time.Duration `yaml:"expect_continue_timeout"` // 100-continue 超时

		MaxIdleConns        int  `yaml:"max_idle_conns"`          // 最大空闲连接数
		MaxIdleConnsPerHost int  `yaml:"max_idle_conns_per_host"` // 每个主机的最大空闲连接数
		MaxConnsPerHost     int  `yaml:"max_conns_per_host"`      // 每个主机的最大连接数
		DisableKeepAlives   bool `yaml:"disable_keepalives"`      // 禁用keepalive
	} `yaml:"http"`

	Monitor struct {
		Path string `yaml:"path"` // 监控路径
	} `yaml:"monitor"`

	ProxyGroups map[string]*ProxyGroupConfig `yaml:"proxygroups"`
	JX          JXConfig                     `yaml:"jx"`
	Reload      int                          `yaml:"reload"` // 添加 Reload 字段
}

type JXConfig struct {
	Path      string                          `yaml:"path"`
	DefaultID string                          `yaml:"default_id"`
	APIGroups map[string]*VideoAPIGroupConfig `yaml:"api_groups"`
}

// VideoAPIGroupConfig 视频解析接口组配置
type VideoAPIGroupConfig struct {
	Endpoints       []string          `yaml:"endpoints"`
	Headers         map[string]string `yaml:"headers"`
	Timeout         time.Duration     `yaml:"timeout"`
	QueryTemplate   string            `yaml:"query_template"`
	Primary         bool              `yaml:"primary"`
	Weight          int               `yaml:"weight"`
	Fallback        bool              `yaml:"fallback"`
	MaxRetries      int               `yaml:"max_retries"`
	Filters         map[string]string `yaml:"filters"`
	Mapping         map[string]string `yaml:"mapping"`
	TVIdRegex       string            `yaml:"tv_id_regex"`
	InfoURLTemplate string            `yaml:"info_url_template"`
}

// ProxyGroupConfig 代理组配置
type ProxyGroupConfig struct {
	Proxies     []*ProxyConfig `yaml:"proxies"`     // 代理服务器列表
	Domains     []string       `yaml:"domains"`     // 域名和IP规则列表(包含IPv4和IPv6)
	IPv6        bool           `yaml:"ipv6"`        // IPv6 开关
	Interval    time.Duration  `yaml:"interval"`    // 检查间隔时间(秒)
	LoadBalance string         `yaml:"loadbalance"` // 负载均衡方式
	MaxRetries  int            `yaml:"max_retries"` // 最大重试次数
	RetryDelay  time.Duration  `yaml:"retry_delay"` // 重试延迟(秒)
	MaxRT       time.Duration  `yaml:"max_rt"`      // 最大响应时间
	Stats       *GroupStats    `yaml:"-"`           // 运行时统计信息
}

// GroupStats 代理组运行时统计
type GroupStats struct {
	LastCheck       time.Time
	ProxyStats      map[string]*ProxyStats
	RoundRobinIndex int
	CurrentIndex    int // 可用于未来其他策略
	sync.RWMutex
}

// ProxyConfig 代理服务器配置
type ProxyConfig struct {
	Name     string            `yaml:"name"`
	Type     string            `yaml:"type"`
	Server   string            `yaml:"server"`
	Port     int               `yaml:"port"`
	UDP      bool              `yaml:"udp"`
	Username string            `yaml:"username"`
	Password string            `yaml:"password"`
	Headers  map[string]string `yaml:"headers"` // 添加自定义headers支持
}

// ProxyStats 代理统计信息
type ProxyStats struct {
	LastCheck     time.Time // 仅测速时更新
	LastUsed      time.Time // 代理被用时更新
	ResponseTime  time.Duration
	Alive         bool
	FailCount     int
	CooldownUntil time.Time
}

// 全局定义测速结果结构体
type TestResult struct {
	Proxy        ProxyConfig
	ResponseTime time.Duration
	Err          error
	StatusCode   int
}

// 重定向缓存 (支持链式存储)
type RedirectChain struct {
	Chain     map[int]string
	ChainHead int
	LastUsed  time.Time
}

var RedirectCache = struct {
	sync.RWMutex
	Mapping map[string]*RedirectChain
}{}

func init() {
	RedirectCache.Mapping = make(map[string]*RedirectChain)
}

//go:embed favicon.ico
var FaviconFile []byte

type CachedGroup struct {
	Group    *ProxyGroupConfig
	LastUsed time.Time
}

var AccessCache = struct {
	sync.RWMutex
	Mapping map[string]*CachedGroup
}{}

func init() {
	AccessCache.Mapping = make(map[string]*CachedGroup)
}

var BufferPools = map[int]*sync.Pool{}

func init() {
	BufferPools = map[int]*sync.Pool{
		8 * 1024: {
			New: func() any { return make([]byte, 8*1024) },
		},
		16 * 1024: {
			New: func() any { return make([]byte, 16*1024) },
		},
		64 * 1024: {
			New: func() any { return make([]byte, 64*1024) },
		},
		128 * 1024: {
			New: func() any { return make([]byte, 128*1024) },
		},
		256 * 1024: {
			New: func() any { return make([]byte, 256*1024) },
		},
		512 * 1024: {
			New: func() any { return make([]byte, 512*1024) },
		},
	}
}

const DefaultDialTimeout = 15 * time.Second

var ErrBrokenPipe = errors.New("broken pipe")

func (c *Config) SetDefaults() {
	// HTTP 默认值
	if c.HTTP.Timeout == 0 {
		c.HTTP.Timeout = 0 // 不限制，适合长连接流
	}
	if c.HTTP.ConnectTimeout == 0 {
		c.HTTP.ConnectTimeout = 10 * time.Second
	}
	if c.HTTP.KeepAlive == 0 {
		c.HTTP.KeepAlive = 10 * time.Second
	}
	if c.HTTP.ResponseHeaderTimeout == 0 {
		c.HTTP.ResponseHeaderTimeout = 10 * time.Second
	}
	if c.HTTP.IdleConnTimeout == 0 {
		c.HTTP.IdleConnTimeout = 5 * time.Second
	}
	if c.HTTP.TLSHandshakeTimeout == 0 {
		c.HTTP.TLSHandshakeTimeout = 10 * time.Second
	}
	if c.HTTP.ExpectContinueTimeout == 0 {
		c.HTTP.ExpectContinueTimeout = 1 * time.Second
	}
	if c.HTTP.MaxIdleConns == 0 {
		c.HTTP.MaxIdleConns = 100
	}
	if c.HTTP.MaxIdleConnsPerHost == 0 {
		c.HTTP.MaxIdleConnsPerHost = 4
	}
	if c.HTTP.MaxConnsPerHost == 0 {
		c.HTTP.MaxConnsPerHost = 8
	}
}

// InitStartTime 初始化程序启动时间
func InitStartTime() {
	initOnce.Do(func() {
		StartTime = time.Now()
	})
}
