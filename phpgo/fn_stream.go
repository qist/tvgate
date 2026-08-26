package phpgo

import (
	"bufio"
	"io"
	"net"
	"strconv"
	"time"
)

func init() {
	// stream_context_create：返回一个上下文资源（纯 Go runtime 中简化为数组占位）
	builtins["stream_context_create"] = func(e *Env, a []Value) (Value, error) {
		return NewArray(), nil
	}
	// fsockopen：建立 TCP 连接，返回资源（fd 整数）
	builtins["fsockopen"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewBool(false), nil
		}
		host := a[0].ToString()
		port := 80
		if len(a) >= 2 {
			port = int(a[1].ToInt())
		}
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 10*time.Second)
		if err != nil {
			return NewBool(false), nil
		}
		fd := e.nextFd
		e.nextFd++
		e.files[fd] = &streamConn{conn: conn, r: bufio.NewReader(conn)}
		return NewInt(int64(fd)), nil
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
	// stream_get_contents：读取流剩余全部内容
	builtins["stream_get_contents"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewString(""), nil
		}
		fd := int(a[0].ToInt())
		sc, ok := e.files[fd].(*streamConn)
		if !ok {
			return NewString(""), nil
		}
		data, err := io.ReadAll(sc.r)
		if err != nil {
			return NewString(""), nil
		}
		return NewString(string(data)), nil
	}
}

// streamConn 适配 net.Conn 到文件句柄表
type streamConn struct {
	conn net.Conn
	r    *bufio.Reader
}

func (s *streamConn) Read(b []byte) (int, error)  { return s.r.Read(b) }
func (s *streamConn) Write(b []byte) (int, error) { return s.conn.Write(b) }
func (s *streamConn) Close() error                { return s.conn.Close() }
