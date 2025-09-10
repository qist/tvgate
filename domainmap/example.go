package domainmap

import (
	"fmt"
	"net/http"
	"time"
)

// ExampleMiddlewareUsage 演示如何使用域名映射中间件
func ExampleMiddlewareUsage() {
	// 创建示例域名映射配置
	// 注意：这里需要使用domainmap.DomainMapConfig而不是config.DomainMapConfig
	mappings := DomainMapList{
		&DomainMapConfig{
			Name:   "cc-to-qist",
			Source: "www.cc.com",
			Target: "www.qist.cc",
		},
		&DomainMapConfig{
			Name:     "http-to-https",
			Source:   "insecure.example.com",
			Target:   "secure.example.com",
			Protocol: "https",
		},
		&DomainMapConfig{
			Name:     "https-to-http",
			Source:   "secure-internal.example.com",
			Target:   "internal.example.com",
			Protocol: "http",
		},
	}

	// 创建HTTP客户端
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	
	// 创建默认处理器
	defaultHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 默认处理逻辑
		fmt.Fprintf(w, "默认处理: 请求 Host: %s, Path: %s", r.Host, r.URL.Path)
	})

	// 创建域名映射器
	mapper := NewDomainMapper(mappings, client, defaultHandler)

	// // 创建示例处理器
	// originalHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	// 	// 示例处理器，输出当前请求的Host和协议
	// 	fmt.Fprintf(w, "请求 Host: %s\n原始 Host: %s\n协议: %s", r.Host, r.Header.Get("X-Original-Host"), r.URL.Scheme)
	// })

	// 创建带有域名映射功能的处理器
	handlerWithDomainMapping := mapper

	// 启动服务器
	http.Handle("/", handlerWithDomainMapping)
	fmt.Println("服务器启动在 :8080 端口")
	http.ListenAndServe(":8080", nil)
}

// ExampleRouterUsage 演示如何使用路由器方式集成域名映射功能
func ExampleRouterUsage() {
	// 这个示例展示如何在主程序中使用路由器方式集成域名映射功能
	// 通常在 main.go 中使用这种方式
	
	// 模拟配置
	// 注意：这里需要使用domainmap.DomainMapConfig而不是config.DomainMapConfig
	mappings := DomainMapList{
		&DomainMapConfig{
			Name:   "cc-to-qist",
			Source: "www.cc.com",
			Target: "www.qist.cc",
		},
	}
	
	// 创建HTTP客户端
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	
	// 创建默认处理器
	defaultHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "默认处理: 请求 Host: %s, Path: %s", r.Host, r.URL.Path)
	})
	
	// 创建域名映射器
	mapper := NewDomainMapper(mappings, client, defaultHandler)
	
	// 启动服务器
	http.Handle("/", mapper)
	fmt.Println("服务器启动在 :8080 端口")
	http.ListenAndServe(":8080", nil)
}