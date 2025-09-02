package groupstats

import (
	"fmt"
	"github.com/qist/tvgate/config"
)

func ValidateConfig(groups map[string]*config.ProxyGroupConfig) error {
	for groupName, group := range groups {
		if group == nil {
			return fmt.Errorf("代理组 %s 为 nil", groupName)
		}
		if group.Proxies == nil {
			return fmt.Errorf("代理组 %s 的 proxies 字段为 nil", groupName)
		}
		for i, proxy := range group.Proxies {
			if proxy == nil {
				return fmt.Errorf("代理组 %s 的第 %d 个代理为 nil", groupName, i)
			}
			if proxy.Name == "" {
				return fmt.Errorf("代理组 %s 的第 %d 个代理名称为空", groupName, i)
			}
		}
	}
	return nil
}
