package proxy

import (
	"github.com/qist/tvgate/config"
)

func GetGroupName(group *config.ProxyGroupConfig) string {
	if len(group.Proxies) > 0 {
		return group.Proxies[0].Name
	}
	return ""
}
