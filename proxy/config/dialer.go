package config

import (
	"bufio"
	"context"
	"fmt"

	"net"
	"net/http"
	"strings"
	"time"
	"golang.org/x/net/proxy"
)

type DialContextWrapper struct {
	Base proxy.Dialer
}

func (d *DialContextWrapper) Dial(network, addr string) (net.Conn, error) {
	// 兼容 proxy.Dialer 接口，调用 DialContext，传入背景 Context
	return d.DialContext(context.Background(), network, addr)
}

func (d *DialContextWrapper) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	type dialResult struct {
		conn net.Conn
		err  error
	}
	resultChan := make(chan dialResult, 1)

	go func() {
		conn, err := d.Base.Dial(network, addr)
		resultChan <- dialResult{conn, err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-resultChan:
		return res.conn, res.err
	}
}

// httpProxyDialer 实现 proxy.Dialer 接口
type HttpProxyDialer struct {
	ProxyAddr string
	Headers   map[string]string
}

func (d *HttpProxyDialer) Dial(network, addr string) (net.Conn, error) {
	// 连接代理服务器
	conn, err := net.DialTimeout("tcp", d.ProxyAddr, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("连接代理失败: %v", err)
	}

	// 构造 CONNECT 请求
	var req strings.Builder
	req.WriteString(fmt.Sprintf("CONNECT %s HTTP/1.1\r\n", addr))

	if len(d.Headers) > 0 {
		// 写入自定义请求头
		for k, v := range d.Headers {
			req.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
		}
	} else {
		// 没有自定义头，写默认 Host
		req.WriteString(fmt.Sprintf("Host: %s\r\n", addr))
	}
	req.WriteString("\r\n")

	// 发送 CONNECT 请求
	if _, err := conn.Write([]byte(req.String())); err != nil {
		conn.Close()
		return nil, fmt.Errorf("发送 CONNECT 请求失败: %v", err)
	}

	// 读取响应
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("读取 CONNECT 响应失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("HTTP 代理 CONNECT 失败: %s", resp.Status)
	}

	return conn, nil
}

type SocksDialerWrapper struct {
	DialFn func(network, addr string) (net.Conn, error)
}

func (w *SocksDialerWrapper) Dial(network, addr string) (net.Conn, error) {
	return w.DialFn(network, addr)
}
