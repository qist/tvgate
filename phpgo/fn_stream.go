package phpgo

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

func init() {
	// stream_context_create：返回一个上下文资源（纯 Go runtime 中简化为数组占位）
	builtins["stream_context_create"] = func(e *Env, a []Value) (Value, error) {
		return NewArray(), nil
	}
	// dialStream 解析 PHP 流地址（[scheme://]host:port）并建立连接。
	// 支持 tcp://（默认）、udp://、ssl://、tls://，超时由 timeout 控制。
	dialStream := func(address string, timeout time.Duration) (net.Conn, error) {
		scheme, rest := "tcp", address
		if idx := indexOfScheme(address); idx >= 0 {
			scheme, rest = address[:idx], address[idx+3:]
		}
		switch scheme {
		case "tcp", "udp":
			return net.DialTimeout(scheme, rest, timeout)
		case "ssl", "tls":
			// IPTV 解析脚本普遍连自签/校验不全的源，与 curl 扩展宽松风格一致：跳过证书校验
			d := &net.Dialer{Timeout: timeout}
			return tls.DialWithDialer(d, "tcp", rest, &tls.Config{InsecureSkipVerify: true})
		default:
			return nil, fmt.Errorf("unsupported scheme %q in %q", scheme, address)
		}
	}
	// registerStream 把连接登记进 fd 表，供 fread/fwrite/fgets/fclose 等复用
	registerStream := func(e *Env, conn net.Conn) Value {
		fd := e.nextFd
		e.nextFd++
		e.files[fd] = &streamConn{conn: conn, r: bufio.NewReader(conn)}
		return NewInt(int64(fd))
	}

	// stream_socket_client(address, &$errno, &$errstr, timeout, flags, context)：
	// 打开互联网/Unix 域套接字连接，返回 stream 资源（fd）或 false。
	// 与 fsockopen 不同：地址是 host:port 整体（支持 ssl:// 等前缀），参数从 2 起可选。
	builtins["stream_socket_client"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewBool(false), nil
		}
		address := a[0].ToString()
		timeout := 60 * time.Second // PHP default_socket_timeout
		if len(a) >= 4 {
			if s := a[3]; s.Kind != KindNull {
				timeout = time.Duration(s.ToFloat() * float64(time.Second))
			}
		}
		conn, err := dialStream(address, timeout)
		if err != nil {
			if len(a) >= 3 {
				writeRef(e, a[1], NewInt(1))
				writeRef(e, a[2], NewString(err.Error()))
			}
			return NewBool(false), nil
		}
		if len(a) >= 3 {
			writeRef(e, a[1], NewInt(0))
			writeRef(e, a[2], NewString(""))
		}
		return registerStream(e, conn), nil
	}
	// fsockopen(host, port, &$errno, &$errstr, timeout)：TCP/TLS 连接，返回 fd 或 false
	builtins["fsockopen"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewBool(false), nil
		}
		host := a[0].ToString()
		port := 80
		if len(a) >= 2 {
			port = int(a[1].ToInt())
		}
		timeout := 10 * time.Second
		if len(a) >= 5 {
			if s := a[4]; s.Kind != KindNull {
				timeout = time.Duration(s.ToFloat() * float64(time.Second))
			}
		}
		address := host
		if schemeIdx := indexOfScheme(host); schemeIdx < 0 {
			// 裸 host:port（fsockopen host 与 port 分开传）
			address = net.JoinHostPort(host, strconv.Itoa(port))
		} else {
			// ssl://host 等形式：补上端口
			address = fmt.Sprintf("%s:%d", host, port)
		}
		conn, err := dialStream(address, timeout)
		if err != nil {
			if len(a) >= 4 {
				writeRef(e, a[2], NewInt(1))
				writeRef(e, a[3], NewString(err.Error()))
			}
			return NewBool(false), nil
		}
		if len(a) >= 4 {
			writeRef(e, a[2], NewInt(0))
			writeRef(e, a[3], NewString(""))
		}
		return registerStream(e, conn), nil
	}
	// fgets：从流读取一行（含换行符）
	builtins["fgets"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewBool(false), nil
		}
		fd := int(a[0].ToInt())
		sc, ok := e.files[fd].(*streamConn)
		if !ok {
			return NewBool(false), nil
		}
		line, err := sc.r.ReadString('\n')
		if err != nil && line == "" {
			return NewBool(false), nil
		}
		return NewString(line), nil
	}
	// feof：判断流是否到末尾（net.Conn 无显式 EOF，默认 false）
	builtins["feof"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewBool(true), nil
		}
		fd := int(a[0].ToInt())
		if _, ok := e.files[fd]; !ok {
			return NewBool(true), nil
		}
		return NewBool(false), nil
	}
	// stream_get_contents：读取流剩余全部内容（支持网络流与本地文件句柄）
	builtins["stream_get_contents"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewString(""), nil
		}
		fd := int(a[0].ToInt())
		sc, ok := e.files[fd].(*streamConn)
		if ok {
			data, err := io.ReadAll(sc.r)
			if err != nil {
				return NewString(""), nil
			}
			return NewString(string(data)), nil
		}
		f, ok := e.files[fd].(*os.File)
		if !ok {
			return NewString(""), nil
		}
		data, err := io.ReadAll(f)
		if err != nil {
			return NewString(""), nil
		}
		return NewString(string(data)), nil
	}
}

// indexOfScheme 返回地址中 "://" 的下标；无 scheme 时返回 -1。
func indexOfScheme(address string) int {
	return strings.Index(address, "://")
}

// streamConn 适配 net.Conn 到文件句柄表
type streamConn struct {
	conn net.Conn
	r    *bufio.Reader
}

func (s *streamConn) Read(b []byte) (int, error)  { return s.r.Read(b) }
func (s *streamConn) Write(b []byte) (int, error) { return s.conn.Write(b) }
func (s *streamConn) Close() error                { return s.conn.Close() }
