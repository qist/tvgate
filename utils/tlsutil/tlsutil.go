// Package tlsutil 提供出站 TLS 客户端的公共套件配置。
package tlsutil

import "crypto/tls"

// rsaKexSuites 静态 RSA 密钥交换套件：Go 1.22 起被移出客户端默认列表
// （GODEBUG tlsrsakex 兼容开关已于 Go 1.27 移除，无法再经 go.mod 恢复），
// 部分旧 CDN 仅支持 TLS1.2 + 静态 RSA 握手，依赖这些套件才能建立连接。
// 显式设置 CipherSuites 不受 GODEBUG 移除影响，全版本行为一致。
var rsaKexSuites = []uint16{
	tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_RSA_WITH_AES_128_CBC_SHA,
	tls.TLS_RSA_WITH_AES_256_CBC_SHA,
}

// CipherSuitesWithRSAKex 返回"静态 RSA 套件 + 当前实现默认套件"的显式列表。
// TLS 1.3 套件不受 CipherSuites 字段控制，始终可用。
func CipherSuitesWithRSAKex() []uint16 {
	suites := make([]uint16, 0, len(rsaKexSuites)+8)
	seen := make(map[uint16]bool, len(rsaKexSuites)+8)
	for _, id := range rsaKexSuites {
		if !seen[id] {
			seen[id] = true
			suites = append(suites, id)
		}
	}
	for _, cs := range tls.CipherSuites() {
		if !seen[cs.ID] {
			seen[cs.ID] = true
			suites = append(suites, cs.ID)
		}
	}
	return suites
}
