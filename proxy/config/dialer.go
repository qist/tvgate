package config

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
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
	done := make(chan struct{})
	var conn net.Conn
	var err error

	go func() {
		conn, err = d.Base.Dial(network, addr)
		close(done)
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-done:
		return conn, err
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
	pool   map[string][]net.Conn // 按 addr 维护连接池
	mu     sync.Mutex
}

func (w *SocksDialerWrapper) Dial(network, addr string) (net.Conn, error) {
	w.mu.Lock()
	conns := w.pool[addr]
	var conn net.Conn
	if len(conns) > 0 {
		conn = conns[len(conns)-1]
		w.pool[addr] = conns[:len(conns)-1]
		w.mu.Unlock()
		// 检查连接是否可用
		one := []byte{}
		conn.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
		if _, err := conn.Read(one); err == nil || err == io.EOF {
			// 连接已关闭或异常，丢弃
			conn.Close()
			conn = nil
		}
		conn.SetReadDeadline(time.Time{})
	} else {
		w.mu.Unlock()
	}
	if conn != nil {
		return conn, nil
	}
	// 新建连接
	c, err := w.DialFn(network, addr)
	return c, err
}

// 归还连接到池
func (w *SocksDialerWrapper) PutConn(addr string, conn net.Conn) {
	w.mu.Lock()
	w.pool[addr] = append(w.pool[addr], conn)
	w.mu.Unlock()
}

// 初始化池
func NewSocksDialerWrapper(fn func(network, addr string) (net.Conn, error)) *SocksDialerWrapper {
	return &SocksDialerWrapper{
		DialFn: fn,
		pool:   make(map[string][]net.Conn),
	}
}
