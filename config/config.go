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
		Port            int      `yaml:"port"` 			  // 监听端口
		CertFile        string   `yaml:"certfile"` 		  // TLS证书文件
		KeyFile         string   `yaml:"keyfile"` 		  // TLS私钥文件
		SSLProtocols    string   `yaml:"ssl_protocols"`  // 支持的TLS协议版本
		SSLCiphers      string   `yaml:"ssl_ciphers"` 	  // 支持的TLS加密算法
		SSLECDHCurve    string   `yaml:"ssl_ecdh_curve"` // 支持的TLS曲线
		MulticastIfaces []string `yaml:"multicast_ifaces"` // 多播网卡列表
	} `yaml:"server"`

	Log struct {
		Enabled    bool   `yaml:"enabled"` 		  // 启用日志
		File       string `yaml:"file"` 		  // 日志文件
		MaxSizeMB  int    `yaml:"maxsize"` 		  // 日志文件最大大小
		MaxBackups int    `yaml:"maxbackups"`  // 最大备份数量
		MaxAgeDays int    `yaml:"maxage"` 		  // 最大保留天数
		Compress   bool   `yaml:"compress"` 		  // 启用压缩
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

	Web struct {
		Enabled  bool   `yaml:"enabled"`  // 启用Web管理界面
		Username string `yaml:"username"` // Web管理用户名
		Password string `yaml:"password"` // Web管理密码
		Path     string `yaml:"path"`     // Web管理路径，默认为/web/
	} `yaml:"web"`

	ProxyGroups map[string]*ProxyGroupConfig `yaml:"proxygroups"` // 代理组配置
	JX          JXConfig                     `yaml:"jx"` // 视频解析配置
	Reload      int                          `yaml:"reload"` // 添加 Reload 字段
}

type JXConfig struct {
	Path      string                          `yaml:"path"` // 视频解析路径
	DefaultID string                          `yaml:"default_id"` // 默认视频ID
	APIGroups map[string]*VideoAPIGroupConfig `yaml:"api_groups"` // 视频API组配置
}

// VideoAPIGroupConfig 视频解析接口组配置
type VideoAPIGroupConfig struct {
	Endpoints       []string          `yaml:"endpoints"` 	// API接口列表
	Headers         map[string]string `yaml:"headers"`   // 自定义请求头
	Timeout         time.Duration     `yaml:"timeout"`  // 请求超时
	QueryTemplate   string            `yaml:"query_template"` // 查询模板
	Primary         bool              `yaml:"primary"`  // 主API标记
	Weight          int               `yaml:"weight"` // 权重
	Fallback        bool              `yaml:"fallback"` // 备用API标记
	MaxRetries      int               `yaml:"max_retries"` // 最大重试次数
	Filters         map[string]string `yaml:"filters"` // 过滤条件
	Mapping         map[string]string `yaml:"mapping"` // 映射
	TVIdRegex       string            `yaml:"tv_id_regex"` // 电视ID正则表达式
	InfoURLTemplate string            `yaml:"info_url_template"` // 电视信息URL模板
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
	Name     string            `yaml:"name"` 		// 代理名称
	Type     string            `yaml:"type"` 		// 代理类型 (http, https, socks5, socks4)
	Server   string            `yaml:"server"` 		// 代理地址 (IP或域名)
	Port     int               `yaml:"port"` 		// 代理端口
	UDP      bool              `yaml:"udp"` 		// UDP支持 (仅socks5)
	Username string            `yaml:"username"`   	// 代理用户名 (可选)
	Password string            `yaml:"password"`  	// 代理密码 (可选)
	Headers  map[string]string `yaml:"headers"` // 添加自定义headers支持
}

// ProxyStats 代理统计信息
type ProxyStats struct {
	LastCheck     time.Time // 仅测速时更新
	LastUsed      time.Time // 代理被用时更新
	ResponseTime  time.Duration // 响应时间
	Alive         bool // 代理是否可用
	FailCount     int // 测速失败次数
	CooldownUntil time.Time // 冷却时间，防止频繁重试
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
