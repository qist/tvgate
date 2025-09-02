package proxy

import (
	"github.com/qist/tvgate/config"
)

// 初始化 Headers，避免 nil
func NormalizeProxyConfig(pc *config.ProxyConfig) {
	if pc.Headers == nil {
		pc.Headers = make(map[string]string)
	}
}
