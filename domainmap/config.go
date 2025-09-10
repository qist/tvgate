package domainmap

// DomainMapConfig 域名映射配置结构
type DomainMapConfig struct {
	Name     string `yaml:"name"`     // 映射名称
	Source   string `yaml:"source"`   // 源域名
	Target   string `yaml:"target"`   // 目标域名
	Protocol string `yaml:"protocol"` // 协议（可选，http或https，空表示保持原协议）
}

// DomainMapList 域名映射配置列表
type DomainMapList []*DomainMapConfig