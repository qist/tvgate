package auth

import (
	"github.com/qist/tvgate/config"
	"time"
)

// DomainMapConfig 域名映射配置结构
type DomainMapConfig struct {
	Name          string            `yaml:"name"`           // 配置名称
	Source        string            `yaml:"source"`         // 源域名
	Target        string            `yaml:"target"`         // 目标域名
	Protocol      string            `yaml:"protocol"`       // 协议 http/https
	Auth          config.AuthConfig `yaml:"auth"`           // 动态/静态 token 配置
	ClientHeaders map[string]string `yaml:"client_headers"` // 前端请求使用
	ServerHeaders map[string]string `yaml:"server_headers"` // 后端请求使用
}

// AuthConfig 授权 token 配置
type AuthConfig struct {
	TokensEnabled  bool         `yaml:"tokens_enabled"`   // 是否启用 token
	TokenParamName string       `yaml:"token_param_name"` // token 参数名
	DynamicTokens  DynamicToken `yaml:"dynamic_tokens"`   // 动态 token 配置
	StaticTokens   StaticToken  `yaml:"static_tokens"`    // 静态 token 列表
}

// DynamicToken 动态 token 配置
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

// DomainMapList 域名映射配置列表
type DomainMapList []*DomainMapConfig
