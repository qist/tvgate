package dns

import (
	"net"
)

func Init() {
	// 自动设置全局DNS解析器
	// 这样所有网络请求都会自动使用自定义DNS解析
	SetupGlobalDNSResolver()
}

func SetupGlobalDNSResolver() {
	// 获取DNS解析器实例
	resolver := GetInstance()
	
	// 设置全局默认解析器为自定义解析器
	// 这样所有标准库的网络操作都会使用我们的DNS解析器
	net.DefaultResolver = resolver.GetNetResolver()
}