package dns

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/qist/tvgate/config"
)

// Init 初始化DNS解析器
func Init() error {
	resolver := GetInstance()
	
	// 如果配置了DNS服务器，则使用自定义解析器，否则回退到系统DNS
	if len(resolver.GetResolvers()) > 0 {
		// 设置全局默认解析器为自定义解析器
		net.DefaultResolver = resolver.GetNetResolver()
		fmt.Printf("INFO: Using configured DNS servers: %v\n", resolver.GetResolvers())
	} else {
		// 回退到系统DNS
		net.DefaultResolver = resolver.GetSystemResolver()
		fmt.Println("INFO: No DNS servers configured, using system DNS resolver")
	}
	
	// 测试DNS解析器
	testHost := "google.com"
	
	// 设置较短的超时时间进行测试
	ctx, cancel := config.ServerCtx, func() {}
	if deadline, ok := config.ServerCtx.Deadline(); !ok || time.Until(deadline) > time.Second*5 {
		ctx, cancel = context.WithTimeout(config.ServerCtx, time.Second*5)
	}
	defer cancel()
	
	// 使用我们自定义的解析器进行测试
	addrs, err := resolver.LookupIPAddr(ctx, testHost)
	if err != nil {
		fmt.Printf("WARNING: DNS resolver not working: %v\n", err)
	} else {
		fmt.Printf("INFO: DNS resolver test lookup for %s: %v\n", testHost, func(addrs []net.IPAddr) []string {
			result := make([]string, len(addrs))
			for i, addr := range addrs {
				result[i] = addr.String()
			}
			return result
		}(addrs))
	}
	
	return nil
}