package dns

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestDNSResolver(t *testing.T) {
	// 初始化DNS解析器
	Init()

	// 测试解析google.com
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	addrs, err := LookupIPAddr(ctx, "google.com")
	if err != nil {
		t.Errorf("解析google.com失败: %v", err)
	} else {
		fmt.Printf("解析google.com成功: %v\n", addrs)
	}

	// 测试解析baidu.com
	addrs2, err := LookupIPAddr(ctx, "baidu.com")
	if err != nil {
		t.Errorf("解析baidu.com失败: %v", err)
	} else {
		fmt.Printf("解析baidu.com成功: %v\n", addrs2)
	}

	// 测试使用net.LookupIP
	ips, err := LookupIP("github.com")
	if err != nil {
		t.Errorf("解析github.com失败: %v", err)
	} else {
		fmt.Printf("解析github.com成功: %v\n", ips)
	}
}

func TestPlainDNSClient(t *testing.T) {
	client := &plainDNSClient{
		server:  "8.8.8.8:53",
		timeout: 5 * time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	addrs, err := client.LookupIPAddr(ctx, "google.com")
	if err != nil {
		t.Errorf("Plain DNS解析google.com失败: %v", err)
	} else {
		fmt.Printf("Plain DNS解析google.com成功: %v\n", addrs)
		if len(addrs) == 0 {
			t.Errorf("Plain DNS解析google.com返回空结果")
		}
	}
}