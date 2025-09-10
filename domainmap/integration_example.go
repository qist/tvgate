package domainmap

import (
	"net/http"
	"time"

	"github.com/qist/tvgate/config"
)

// IntegrateWithMainHandler 集成域名映射到主处理程序的示例
func IntegrateWithMainHandler(originalHandler http.Handler) http.Handler {
	// 从主配置中获取域名映射配置
	// 需要将config.DomainMapConfig转换为domainmap.DomainMapConfig
	var mappings DomainMapList
	if len(config.Cfg.DomainMap) > 0 {
		mappings = make(DomainMapList, len(config.Cfg.DomainMap))
		for i, mapping := range config.Cfg.DomainMap {
			mappings[i] = &DomainMapConfig{
				Name:     mapping.Name,
				Source:   mapping.Source,
				Target:   mapping.Target,
				Protocol: mapping.Protocol,
			}
		}
	}
	
	// 如果没有配置域名映射，则直接返回原始处理器
	if len(mappings) == 0 {
		return originalHandler
	}
	
	// 创建HTTP客户端
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	
	// 创建域名映射器
	mapper := NewDomainMapper(mappings, client, originalHandler)
	
	// 返回带有域名映射功能的处理器
	return mapper
}