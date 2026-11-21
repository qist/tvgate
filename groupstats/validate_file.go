package groupstats

import (
	"fmt"

	"github.com/qist/tvgate/config"
)

// ValidateConfig 校验代理组配置。
// pool 模式下 proxies 可以为空，只要 pool.servers 非空即可。
func ValidateConfig(groups map[string]*config.ProxyGroupConfig) error {
	for groupName, group := range groups {
		if group == nil {
			return fmt.Errorf("代理组 %s 为 nil", groupName)
		}
		hasProxies := len(group.Proxies) > 0
		hasPool := group.Pool != nil && len(group.Pool.Servers) > 0
		if !hasProxies && !hasPool {
			return fmt.Errorf("代理组 %s 至少需要配置 proxies 或 pool.servers", groupName)
		}
		for i, proxy := range group.Proxies {
			if proxy == nil {
				return fmt.Errorf("代理组 %s 的第 %d 个代理为 nil", groupName, i)
			}
			if proxy.Name == "" {
				return fmt.Errorf("代理组 %s 的第 %d 个代理名称为空", groupName, i)
			}
		}
		if group.Pool != nil {
			if err := group.Pool.Validate(); err != nil {
				return fmt.Errorf("代理组 %s 的 pool 配置无效: %w", groupName, err)
			}
		}
	}
	return nil
}
