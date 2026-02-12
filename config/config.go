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
		Port                 int           `yaml:"port"`                   // 旧端口
		HTTPPort             int           `yaml:"http_port"`              // HTTP 可配置端口
		CertFile             string        `yaml:"certfile"`               // TLS证书文件
		KeyFile              string        `yaml:"keyfile"`                // TLS私钥文件
		SSLProtocols         string        `yaml:"ssl_protocols"`          // 支持的TLS协议版本
		SSLCiphers           string        `yaml:"ssl_ciphers"`            // 支持的TLS加密算法
		SSLECDHCurve         string        `yaml:"ssl_ecdh_curve"`         // 支持的TLS曲线
		TLS                  TLSConfig     `yaml:"tls"`                    // TLS 配置
		HTTPToHTTPS          bool          `yaml:"http_to_https"`          // HTTP 跳转 HTTPS
		MulticastIfaces      []string      `yaml:"multicast_ifaces"`       // 多播网卡
		McastRejoinInterval  time.Duration `yaml:"mcast_rejoin_interval"`  // 多播重连间隔时间
		FccType              string        `yaml:"fcc_type"`               // FCC类型: telecom, huawei
		FccCacheSize         int           `yaml:"fcc_cache_size"`         // FCC缓存大小，默认16384
		FccListenPortMin     int           `yaml:"fcc_listen_port_min"`    // FCC监听端口范围最小值
		FccListenPortMax     int           `yaml:"fcc_listen_port_max"`    // FCC监听端口范围最大值
		UpstreamInterface    string        `yaml:"upstream_interface"`     // 默认上游接口
		UpstreamInterfaceFcc string        `yaml:"upstream_interface_fcc"` // FCC专用上游接口
		TS                   TSConfig      `yaml:"ts"`                     // TS 配置
	} `yaml:"server"`

	Log struct {
		Enabled    bool   `yaml:"enabled"`    // 启用日志
		File       string `yaml:"file"`       // 日志文件
		MaxSizeMB  int    `yaml:"maxsize"`    // 日志文件最大大小
		MaxBackups int    `yaml:"maxbackups"` // 最大备份数量
		MaxAgeDays int    `yaml:"maxage"`     // 最大保留天数
		Compress   bool   `yaml:"compress"`   // 启用压缩
	} `yaml:"log"`

	HTTP struct {
		Timeout               time.Duration `yaml:"timeout"`                 // 整体请求超时 (0 = 不限制)
		ConnectTimeout        time.Duration `yaml:"connect_timeout"`         // TCP连接超时
		KeepAlive             time.Duration `yaml:"keepalive"`               // TCP保活
		ResponseHeaderTimeout time.Duration `yaml:"response_header_timeout"` // 等响应头超时
		IdleConnTimeout       time.Duration `yaml:"idle_conn_timeout"`       // 空闲连接超时
		TLSHandshakeTimeout   time.Duration `yaml:"tls_handshake_timeout"`   // TLS握手超时
		ExpectContinueTimeout time.Duration `yaml:"expect_continue_timeout"` // 100-continue 超时

		MaxIdleConns        int   `yaml:"max_idle_conns"`          // 最大空闲连接数
		MaxIdleConnsPerHost int   `yaml:"max_idle_conns_per_host"` // 每个主机的最大空闲连接数
		MaxConnsPerHost     int   `yaml:"max_conns_per_host"`      // 每个主机的最大连接数
		DisableKeepAlives   *bool `yaml:"disable_keepalives"`      // 禁用keepalive
		InsecureSkipVerify  *bool `yaml:"insecure_skip_verify"`    // 是否跳过TLS验证
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

	// DNS配置
	DNS DNSConfig `yaml:"dns"`

	// GitHub 加速配置
	Github GithubConfig `yaml:"github"`

	// 全局认证配置
	GlobalAuth AuthConfig `yaml:"global_auth"`
	// 域名映射配置
	DomainMap []*DomainMapConfig `yaml:"domainmap"`

	ProxyGroups map[string]*ProxyGroupConfig `yaml:"proxygroups"` // 代理组配置
	JX          JXConfig                     `yaml:"jx"`          // 视频解析配置
	Reload      int                          `yaml:"reload"`      // 添加 Reload 字段

	// Publisher配置 - 修改为直接包含streams
	Publisher *PublisherConfig `yaml:"publisher"` // 推流配置
}

type TSConfig struct {
	Enable    *bool         `yaml:"enable"`     // 是否启用 TS 缓存
	CacheSize int           `yaml:"cache_size"` // TS缓存大小，默认128MB
	CacheTTL  time.Duration `yaml:"cache_ttl"`  // TS缓存TTL，默认2分钟
}

// PublisherConfig represents the publisher configuration structure
type PublisherConfig struct {
	Path string `yaml:"path"`
	// 注意：这里直接包含streams而不是嵌套在Streams字段中
	Streams map[string]*StreamItem `yaml:",inline,omitempty"`
}

// StreamItem represents a single stream configuration item
type StreamItem struct {
	BufferSize int        `yaml:"buffer_size,omitempty"`
	Protocol   string     `yaml:"protocol"`
	Enabled    bool       `yaml:"enabled"`
	StreamKey  StreamKey  `yaml:"streamkey,omitempty"`
	Stream     StreamData `yaml:"stream"`
}

// StreamKey represents the stream key configuration
type StreamKey struct {
	Type       string `yaml:"type"`                 // "random", "fixed" or "external"
	Value      string `yaml:"value,omitempty"`      // for fixed type
	Length     int    `yaml:"length,omitempty"`     // for random type
	Expiration string `yaml:"expiration,omitempty"` // 过期时间（支持字符串格式，如"24h"）
}

// FFmpegOptions represents flexible ffmpeg options configuration
type FFmpegOptions struct {
	GlobalArgs     []string       `yaml:"global_args,omitempty"`      // 全局参数
	InputPreArgs   []string       `yaml:"input_pre_args,omitempty"`   // 输入前参数
	InputPostArgs  []string       `yaml:"input_post_args,omitempty"`  // 输入后参数
	Filters        *FilterOptions `yaml:"filters,omitempty"`          // 滤镜配置
	VideoCodec     string         `yaml:"video_codec,omitempty"`      // 视频编码器
	AudioCodec     string         `yaml:"audio_codec,omitempty"`      // 音频编码器
	VideoBitrate   string         `yaml:"video_bitrate,omitempty"`    // 视频码率
	AudioBitrate   string         `yaml:"audio_bitrate,omitempty"`    // 音频码率
	Preset         string         `yaml:"preset,omitempty"`           // 编码预设
	CRF            int            `yaml:"crf,omitempty"`              // CRF值
	OutputFormat   string         `yaml:"output_format,omitempty"`    // 封装格式
	OutputPreArgs  []string       `yaml:"output_pre_args,omitempty"`  // 输出前参数
	OutputPostArgs []string       `yaml:"output_post_args,omitempty"` // 输出后参数
	CustomArgs     []string       `yaml:"custom_args,omitempty"`      // 自定义参数
	UserAgent      string         `yaml:"user_agent,omitempty"`       // User-Agent
	Headers        []string       `yaml:"headers,omitempty"`          // 自定义请求头
	StreamCopy     bool           `yaml:"stream_copy,omitempty"`      // 流复制模式（不重新编码）
	UseReFlag      bool           `yaml:"use_re_flag,omitempty"`      // 是否使用-re参数（以本地帧速率读取输入）
	PixFmt         string         `yaml:"pix_fmt,omitempty"`          // 像素格式，如 yuv420p
	GopSize        int            `yaml:"gop_size,omitempty"`         // GOP大小
}

// FilterOptions represents video and audio filter configurations
type FilterOptions struct {
	VideoFilters []string `yaml:"video_filters,omitempty"` // 视频滤镜链
	AudioFilters []string `yaml:"audio_filters,omitempty"` // 音频滤镜链
}

// StreamData represents stream source configuration
type StreamData struct {
	Source        SourceData    `yaml:"source"`
	LocalPlayUrls []PlayOutput  `yaml:"local_play_urls"` // flv hls
	Mode          string        `yaml:"mode"`            // "primary-backup" or "all"
	Receivers     ReceiversData `yaml:"receivers"`
}

// SourceData represents the source stream configuration
type SourceData struct {
	Type          string         `yaml:"type,omitempty"`
	URL           string         `yaml:"url"`
	BackupURL     string         `yaml:"backup_url,omitempty"`
	FFmpegOptions *FFmpegOptions `yaml:"ffmpeg_options,omitempty"` //独立推流参数
}

// PlayOutput represents play URLs for different protocols
type PlayOutput struct {
	Protocol           string         `yaml:"protocol"` // flv/hls/…
	Enabled            bool           `yaml:"enabled"`
	FlvFFmpegOptions   *FFmpegOptions `yaml:"flv_ffmpeg_options,omitempty"`
	HlsFFmpegOptions   *FFmpegOptions `yaml:"hls_ffmpeg_options,omitempty"`
	HlsSegmentDuration int            `yaml:"hls_segment_duration,omitempty"` // HLS片段时长（秒）
	HlsSegmentCount    int            `yaml:"hls_segment_count,omitempty"`    // 保留的HLS片段数量
	HlsPath            string         `yaml:"hls_path,omitempty"`             // HLS文件存储路径
	HlsEnablePlayback  bool           `yaml:"hls_enable_playback,omitempty"`  // 是否开启回放模式
	HlsRetentionDays   time.Duration  `yaml:"hls_retention_days,omitempty"`   // TS 文件保留天数
	TSFilenameTemplate string         `yaml:"ts_filename_template,omitempty"` // TS 文件名模板
}

// PlayUrls represents play URLs for different protocols
type PlayUrls struct {
	Flv string `yaml:"flv,omitempty"`
	Hls string `yaml:"hls,omitempty"`
}

// ReceiversData represents either a primary-backup or all receiver configuration
type ReceiversData struct {
	Primary *ReceiverItem  `yaml:"primary,omitempty"`
	Backup  *ReceiverItem  `yaml:"backup,omitempty"`
	All     []ReceiverItem `yaml:"all,omitempty"`
}

// ReceiverItem represents a receiver configuration
type ReceiverItem struct {
	PushURL       string         `yaml:"push_url"`
	PlayUrls      PlayUrls       `yaml:"play_urls,omitempty"`
	FFmpegOptions *FFmpegOptions `yaml:"ffmpeg_options,omitempty"` //独立推流参数
}

// DNSConfig DNS配置
type DNSConfig struct {
	Servers  []string      `yaml:"servers"`   // DNS服务器列表
	Timeout  time.Duration `yaml:"timeout"`   // DNS查询超时时间
	MaxConns int           `yaml:"max_conns"` // 最大连接数
}

type TLSConfig struct {
	HTTPSPort int    `yaml:"https_port"`
	CertFile  string `yaml:"certfile"`
	KeyFile   string `yaml:"keyfile"`
	Protocols string `yaml:"ssl_protocols"`
	Ciphers   string `yaml:"ssl_ciphers"`
	ECDHCurve string `yaml:"ssl_ecdh_curve"`
	EnableH3  bool   `yaml:"enable_h3"` // 新增 HTTP/3 开关
}

// DomainMapConfig 域名映射配置结构
type DomainMapConfig struct {
	Name          string            `yaml:"name"`           // 配置名称
	Source        string            `yaml:"source"`         // 源域名
	Target        string            `yaml:"target"`         // 目标域名
	Protocol      string            `yaml:"protocol"`       // 协议 http/https
	Auth          AuthConfig        `yaml:"auth"`           // 动态/静态 token 配置
	ClientHeaders map[string]string `yaml:"client_headers"` // 前端请求使用
	ServerHeaders map[string]string `yaml:"server_headers"` // 后端请求使用
}

// GithubConfig GitHub 加速配置
type GithubConfig struct {
	Enabled    bool          `yaml:"enabled"`     // 是否启用
	URL        string        `yaml:"url"`         // 主加速地址
	BackupURLs []string      `yaml:"backup_urls"` // 备用加速地址
	Timeout    time.Duration `yaml:"timeout"`     // 请求超时时间
	Retry      int           `yaml:"retry"`       // 最大重试次数
}

// AuthConfig 授权 token 配置
type AuthConfig struct {
	TokensEnabled  bool         `yaml:"tokens_enabled"`   // 是否启用 token
	TokenParamName string       `yaml:"token_param_name"` // token 参数名
	DynamicTokens  DynamicToken `yaml:"dynamic_tokens"`   // 动态 token 配置
	StaticTokens   StaticToken  `yaml:"static_tokens"`    // 静态 token 列表
}

// DynamicTokenConfig 动态 token 配置
type DynamicToken struct {
	EnableDynamic bool          `yaml:"enable_dynamic"` // 是否启用动态 token
	DynamicTTL    time.Duration `yaml:"dynamic_ttl"`    // 动态 token 有效期，例如 1h
	Secret        string        `yaml:"secret"`         // AES key
	Salt          string        `yaml:"salt"`           // salt
}

// StaticToken 静态 token 配置
type StaticToken struct {
	Token        string        `yaml:"token"`         // token 值
	ExpireHours  time.Duration `yaml:"expire_hours"`  // 例如 30s, 24h
	EnableStatic bool          `yaml:"enable_static"` // 是否启用静态 token
}
type JXConfig struct {
	Path      string                          `yaml:"path"`       // 视频解析路径
	DefaultID string                          `yaml:"default_id"` // 默认视频ID
	APIGroups map[string]*VideoAPIGroupConfig `yaml:"api_groups"` // 视频API组配置
}

// VideoAPIGroupConfig 视频解析接口组配置
type VideoAPIGroupConfig struct {
	Endpoints     []string          `yaml:"endpoints"`      // API接口列表
	Timeout       time.Duration     `yaml:"timeout"`        // 请求超时
	QueryTemplate string            `yaml:"query_template"` // 查询模板
	Primary       bool              `yaml:"primary"`        // 主API标记
	Weight        int               `yaml:"weight"`         // 权重
	Fallback      bool              `yaml:"fallback"`       // 备用API标记
	MaxRetries    int               `yaml:"max_retries"`    // 最大重试次数
	Filters       map[string]string `yaml:"filters"`        // 过滤条件
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
	Name     string            `yaml:"name"`     // 代理名称
	Type     string            `yaml:"type"`     // 代理类型 (http, https, socks5, socks4)
	Server   string            `yaml:"server"`   // 代理地址 (IP或域名)
	Port     int               `yaml:"port"`     // 代理端口
	UDP      bool              `yaml:"udp"`      // UDP支持 (仅socks5)
	Username string            `yaml:"username"` // 代理用户名 (可选)
	Password string            `yaml:"password"` // 代理密码 (可选)
	Headers  map[string]string `yaml:"headers"`  // 添加自定义headers支持
}

// ProxyStats 代理统计信息
type ProxyStats struct {
	LastCheck     time.Time     // 仅测速时更新
	LastUsed      time.Time     // 代理被用时更新
	ResponseTime  time.Duration // 响应时间
	Alive         bool          // 代理是否可用
	FailCount     int           // 测速失败次数
	CooldownUntil time.Time     // 冷却时间，防止频繁重试
	StatusCode    int           // 测试返回状态码（HTTP/自定义）
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
	if c.HTTP.DisableKeepAlives == nil {
		c.HTTP.DisableKeepAlives = ptr(false)
	}
	if c.HTTP.InsecureSkipVerify == nil {
		c.HTTP.InsecureSkipVerify = ptr(false)
	}
	// Server 默认值
	if c.Server.FccListenPortMin == 0 {
		c.Server.FccListenPortMin = 40000
	}
	if c.Server.FccListenPortMax == 0 {
		c.Server.FccListenPortMax = 40100

	}
	// 设置默认值
	if c.Server.FccCacheSize <= 0 {
		c.Server.FccCacheSize = 16384
	}

	//TS 缓存开关
	if c.Server.TS.Enable == nil {
		c.Server.TS.Enable = ptr(false)
	}
	// TS缓存默认值
	if c.Server.TS.CacheSize <= 0 {
		c.Server.TS.CacheSize = 128 // 默认128MB
	}
	if c.Server.TS.CacheTTL <= 0 {
		c.Server.TS.CacheTTL = 2 * time.Minute // 默认2分钟
	}

	// DNS 默认值
	if c.DNS.Timeout == 0 {
		c.DNS.Timeout = 5 * time.Second
	}
	if c.DNS.MaxConns == 0 {
		c.DNS.MaxConns = 10
	}

	// GitHub 默认值
	if c.Github.Timeout == 0 {
		c.Github.Timeout = 10 * time.Second
	}
	if c.Github.Retry == 0 {
		c.Github.Retry = 3
	}
}

// InitStartTime 初始化程序启动时间
func InitStartTime() {
	initOnce.Do(func() {
		StartTime = time.Now()
	})
}

func ptr[T any](v T) *T { return &v }
