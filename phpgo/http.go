package phpgo

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
	pgdns "github.com/qist/tvgate/dns"
)

// ServePHP 执行 PHP 源码并写入 HTTP 响应。
// src 为脚本源码；req 可为 nil（命令行/测试用）。
func ServePHP(env *Env, w http.ResponseWriter, src string) error {
	// 重置输出缓冲
	env.echoOut.Reset()
	env.headers = nil
	env.exitLoc = false
	env.statusCode = 0
	env.statusCodeSet = false
	// 重置按请求状态
	env.callArgs = nil
	env.shutdownFuncs = nil
	env.errorHandler = NewNull()
	env.errorHandlers = nil
	env.errorMask = 0
	env.lastErrLevel = 0
	env.lastErrMsg = ""
	env.headerCallbacks = nil

	prog, err := ParseProgram(src)
	if err != nil {
		if w != nil {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "PHP Parse Error: %v", err)
		}
		return err
	}
	if _, err := env.Run(prog); err != nil {
		if w != nil {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "PHP Fatal Error: %v", err)
		}
		return err
	}
	if w == nil {
		return nil
	}
	// 调用 header_register_callback 注册的回调（发送前），随后写出全部 header()
	for _, cb := range env.headerCallbacks {
		if cb.Kind != KindNull {
			_, _ = callCallable(env, cb, nil)
		}
	}
	for _, h := range env.headers {
		if i := strings.IndexByte(h, ':'); i > 0 {
			name := strings.TrimSpace(h[:i])
			val := strings.TrimSpace(h[i+1:])
			// setcookie 可能设置多个 Set-Cookie，需用 Add 累加（Go Header.Set 会覆盖同名）
			if strings.EqualFold(name, "Set-Cookie") {
				w.Header().Add(name, val)
			} else {
				w.Header().Set(name, val)
			}
		}
	}
	ct := w.Header().Get("Content-Type")
	// 未显式设置 Content-Type 时默认 text/html
	if ct == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	} else if !strings.Contains(strings.ToLower(ct), "charset=") {
		// 与 PHP 默认 default_charset(=UTF-8) 行为一致：脚本设了 Content-Type 但未带 charset 时补 UTF-8，
		// 避免浏览器/播放器按本地 GBK 解读 UTF-8 输出造成中文乱码。
		// 已带 charset（如 charset=gbk）的保持不变，兼容 GBK 脚本。
		w.Header().Set("Content-Type", ct+";charset=UTF-8")
	}
	// 决定最终状态码：
	//  - 脚本显式 header("HTTP/1.x NNN ...") 优先；
	//  - 否则若设置了 Location 头（PHP 约定），自动转为 302 Found；
	//  - 否则 200 OK。
	status := http.StatusOK
	if env.statusCodeSet {
		status = env.statusCode
	} else if w.Header().Get("Location") != "" {
		status = http.StatusFound // 302
	}
	w.WriteHeader(status)
	io.WriteString(w, env.echoOut.String())
	return nil
}

// defaultProxy 是 ProxyFunc 的默认实现：用 Go 标准库发请求（支持代理）。
// 由外部注入 *http.Client（含代理 transport）。
func defaultProxy(client *http.Client) ProxyFunc {
	return func(method, u string, opts *CurlOptions) (*ProxyResult, error) {
		// 根据选项动态构建 client
		c := client
		if c == nil {
			c = http.DefaultClient
		}
		// 需要自定义 TLS/超时/重定向/IP 族时创建独立 client（共享 client 的 transport 无法逐请求改 TLS）
		if opts != nil && (opts.SkipSSL || opts.SkipHostVerify || opts.FollowRedirect ||
			opts.TimeoutFloat > 0 || opts.ConnectTimeoutFloat > 0 || opts.IPResolve != 0 ||
			opts.TLSVersion != 0 || opts.CAFile != "" || opts.CAPath != "" ||
			opts.CertFile != "" || opts.KeyFile != "" || opts.MaxRedirects > 0 || opts.ForbidReuse) {
			tlsCfg, err := buildCurlTLSConfig(opts)
			if err != nil {
				return nil, err
			}
			transport := &http.Transport{
				TLSClientConfig: tlsCfg,
			}
			// CURLOPT_IPRESOLVE：强制 v4/v6 解析
			if opts.IPResolve != 0 {
				transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
					host, port, err := net.SplitHostPort(addr)
					if err != nil {
						return nil, err
					}
					ips, err := pgdns.GetInstance().LookupIP(host)
					if err != nil || len(ips) == 0 {
						return nil, fmt.Errorf("curl IPRESOLVE 解析失败: %w", err)
					}
					var chosen net.IP
					for _, ip := range ips {
						v4 := ip.To4()
						if opts.IPResolve == 1 && v4 != nil { // CURL_IPRESOLVE_V4
							chosen = v4
							break
						}
						if opts.IPResolve == 2 && v4 == nil { // CURL_IPRESOLVE_V6
							chosen = ip
							break
						}
					}
					if chosen == nil {
						return nil, fmt.Errorf("curl IPRESOLVE: 无匹配 %d 的地址", opts.IPResolve)
					}
					var d net.Dialer
					return d.DialContext(ctx, network, net.JoinHostPort(chosen.String(), port))
				}
			}
			// CURLOPT_FORBID_REUSE：禁用连接复用
			if opts.ForbidReuse {
				transport.DisableKeepAlives = true
			}
			c = &http.Client{
				Transport: transport,
			}
			// 浮点超时优先
			if opts.TimeoutFloat > 0 {
				c.Timeout = time.Duration(opts.TimeoutFloat * float64(time.Second))
			} else if opts.Timeout > 0 {
				c.Timeout = time.Duration(opts.Timeout) * time.Second
			}
			// 连接超时
			if opts.ConnectTimeoutFloat > 0 {
				transport.DialContext = nil // 使用默认拨号
				transport.TLSHandshakeTimeout = time.Duration(opts.ConnectTimeoutFloat * float64(time.Second))
				transport.ResponseHeaderTimeout = time.Duration(opts.ConnectTimeoutFloat * float64(time.Second))
			}
			// 重定向：严格按 CURLOPT_FOLLOWLOCATION / CURLOPT_MAXREDIRS 控制
			switch {
			case !opts.FollowRedirect:
				c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
					return http.ErrUseLastResponse
				}
			case opts.MaxRedirects > 0:
				max := opts.MaxRedirects
				c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
					if len(via) >= max {
						return fmt.Errorf("curl: 超过最大重定向次数 %d", max)
					}
					return nil
				}
			}
		}
		var body io.Reader
		if opts != nil && opts.PostData != "" {
			body = strings.NewReader(opts.PostData)
		}
		req, err := http.NewRequest(method, u, body)
		if err != nil {
			return nil, err
		}
		// 捕获实际连接的对端 IP（CURLINFO_PRIMARY_IP）：
		// 包裹 transport 的 DialContext，记录每次拨号目标的对端 IP。
		var primaryIP string
		if c != nil {
			if tr, ok := c.Transport.(*http.Transport); ok {
				origDial := tr.DialContext
				tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
					var conn net.Conn
					var err error
					if origDial != nil {
						conn, err = origDial(ctx, network, addr)
					} else {
						conn, err = (&net.Dialer{}).DialContext(ctx, network, addr)
					}
					if err == nil && conn != nil {
						if ra := conn.RemoteAddr(); ra != nil {
							primaryIP = ra.String()
						}
					}
					return conn, err
				}
				defer func() { tr.DialContext = origDial }() // 请求结束后还原，避免影响其它并发请求
			}
		}
		if opts != nil {
			for _, h := range opts.Headers {
				if i := strings.IndexByte(h, ':'); i > 0 {
					key := strings.TrimSpace(h[:i])
					val := strings.TrimSpace(h[i+1:])
					// Go http 中 Host 头需设置 req.Host，而非 req.Header.Set("Host", ...)
					if strings.EqualFold(key, "Host") {
						req.Host = val
						continue
					}
					req.Header.Set(key, val)
				}
			}
			if opts.UserAgent != "" {
				req.Header.Set("User-Agent", opts.UserAgent)
			}
			// CURLOPT_REFERER
			if opts.Referer != "" {
				req.Header.Set("Referer", opts.Referer)
			}
			// CURLOPT_COOKIE（含 COOKIEFILE 合并后的值）
			if opts.Cookie != "" {
				req.Header.Set("Cookie", opts.Cookie)
			}
			// CURLOPT_ENCODING："" 表示由 Go 自动处理 gzip；显式指定则按值发 Accept-Encoding
			if opts.Encoding != "" {
				req.Header.Set("Accept-Encoding", opts.Encoding)
			}
			// CURLOPT_VERBOSE
			if opts.Verbose {
				fmt.Printf("curl: %s %s (ua=%s)\n", method, u, opts.UserAgent)
			}
			// PHP curl: 当 CURLOPT_POSTFIELDS 为字符串且未显式设置 Content-Type 时，
			// 自动设为 application/x-www-form-urlencoded
			if opts.HasPostData && opts.PostData != "" {
				hasCT := false
				for _, h := range opts.Headers {
					if i := strings.IndexByte(h, ':'); i > 0 {
						if strings.EqualFold(strings.TrimSpace(h[:i]), "Content-Type") {
							hasCT = true
							break
						}
					}
				}
				if !hasCT {
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				}
			}
		}
		resp, err := c.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		// CURLOPT_FAILONERROR：HTTP >= 400 视为错误（对齐 PHP curl，返回 false）
		if opts != nil && opts.FailOnError && resp.StatusCode >= 400 {
			return nil, fmt.Errorf("curl: HTTP %d %s", resp.StatusCode, resp.Status)
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		// 只要请求里带上了 Accept-Encoding（无论来自 CURLOPT_ENCODING 还是 CURLOPT_HTTPHEADER），
		// Go 的 Transport 都不会自动解压（它仅在自己添加 Accept-Encoding: gzip 时才透明解压），
		// 这里统一按 Content-Encoding 手动解压，对齐 PHP curl 的行为。
		if req.Header.Get("Accept-Encoding") != "" {
			if enc := resp.Header.Get("Content-Encoding"); enc != "" {
				if d, ok := decodeCurlBody(data, enc); ok {
					data = d
					// 解压后移除 Content-Encoding / Content-Length，与 Go 自动解压时的表现一致
					resp.Header.Del("Content-Encoding")
					resp.Header.Del("Content-Length")
				}
			}
		}
		return &ProxyResult{
			Body:         string(data),
			StatusCode:   resp.StatusCode,
			Location:     resp.Header.Get("Location"),
			ContentType:  resp.Header.Get("Content-Type"),
			EffectiveURL: resp.Request.URL.String(),
			Headers:      headerLines(resp.Header),
			PrimaryIP:    primaryIP,
		}, nil
	}
}

// buildCurlTLSConfig 按 curl TLS 选项构建 tls.Config。
func buildCurlTLSConfig(opts *CurlOptions) (*tls.Config, error) {
	tlsCfg := &tls.Config{InsecureSkipVerify: opts.SkipSSL || opts.SkipHostVerify}
	if opts.TLSVersion != 0 {
		tlsCfg.MinVersion = opts.TLSVersion
	}
	if opts.CAFile != "" || opts.CAPath != "" {
		pool := x509.NewCertPool()
		if opts.CAFile != "" {
			pem, err := os.ReadFile(opts.CAFile)
			if err != nil {
				return nil, fmt.Errorf("curl: 读取 CAINFO 失败: %w", err)
			}
			if !pool.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("curl: CAINFO 无有效证书: %s", opts.CAFile)
			}
		}
		if opts.CAPath != "" {
			entries, err := os.ReadDir(opts.CAPath)
			if err != nil {
				return nil, fmt.Errorf("curl: 读取 CAPATH 失败: %w", err)
			}
			for _, ent := range entries {
				if ent.IsDir() {
					continue
				}
				if pem, err := os.ReadFile(filepath.Join(opts.CAPath, ent.Name())); err == nil {
					pool.AppendCertsFromPEM(pem)
				}
			}
		}
		tlsCfg.RootCAs = pool
	}
	if opts.CertFile != "" && opts.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(opts.CertFile, opts.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("curl: 加载客户端证书失败: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	return tlsCfg, nil
}

// decodeCurlBody 按 Content-Encoding 手动解压响应体，支持 gzip / deflate / br 及组合编码。
// 返回 (解压后的数据, 是否确实发生了解压)；编码不支持或解压失败时返回原始数据与 false。
func decodeCurlBody(data []byte, encoding string) ([]byte, bool) {
	decoded := false
	for _, enc := range strings.Split(encoding, ",") {
		switch strings.ToLower(strings.TrimSpace(enc)) {
		case "gzip", "x-gzip":
			gz, err := gzip.NewReader(bytes.NewReader(data))
			if err != nil {
				return data, decoded
			}
			d, derr := io.ReadAll(gz)
			_ = gz.Close()
			if derr != nil {
				return data, decoded
			}
			data, decoded = d, true
		case "deflate":
			zr, err := zlib.NewReader(bytes.NewReader(data))
			if err != nil {
				return data, decoded
			}
			d, derr := io.ReadAll(zr)
			_ = zr.Close()
			if derr != nil {
				return data, decoded
			}
			data, decoded = d, true
		case "br":
			d, err := io.ReadAll(brotli.NewReader(bytes.NewReader(data)))
			if err != nil {
				return data, decoded
			}
			data, decoded = d, true
		}
	}
	return data, decoded
}

// headerLines 把 http.Header 转为 "Key: Value" 行（按键排序，保证确定性输出）
func headerLines(h http.Header) []string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(h))
	for _, k := range keys {
		for _, v := range h[k] {
			out = append(out, k+": "+v)
		}
	}
	return out
}

// NewDefaultEnv 用默认代理（Go net/http）创建执行环境。
func NewDefaultEnv(client *http.Client) *Env {
	return NewEnv(defaultProxy(client))
}

// ---------------------------------------------------------------------------
// 内置函数：HTTP / 文件读取
// ---------------------------------------------------------------------------

// fileGetContents 实现 file_get_contents($url) 与读取本地文件。
func fileGetContents(env *Env, vs []Value) (Value, error) {
	if len(vs) == 0 {
		return NewNull(), nil
	}
	path := vs[0].ToString()
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		result, err := env.proxy("GET", path, &CurlOptions{})
		if err != nil {
			// PHP 语义：失败返回 false（可被 @ 抑制），而非致命错误，脚本可用 !== false 重试
			return NewBool(false), nil
		}
		return NewString(result.Body), nil
	}
	if path == "php://input" {
		return NewString(env.phpInput), nil
	}
	// 相对路径相对于脚本目录解析
	path = env.ResolvePath(path)
	data, err := os_ReadFile(path)
	if err != nil {
		// PHP 语义：文件不存在/不可读返回 false（可被 @ 抑制）
		return NewBool(false), nil
	}
	return NewString(string(data)), nil
}

// writeRef 若第 i 个参数是引用（&$var），把 v 写回该变量
func writeRef(env *Env, refVal Value, v Value) {
	if refVal.Kind == KindRef && refVal.Ref != nil {
		refVal.Ref.assign(env, v)
	}
}

// ---------------------------------------------------------------------------
// 辅助：assignable 接口用于 &$matches 形参
// ---------------------------------------------------------------------------

type assignable interface {
	assign(env *Env, v Value)
	value(env *Env) Value
}

// varRef 表示一个变量引用（用于 preg_match 的引用参数）
type varRef struct {
	name string
}

func (r *varRef) assign(env *Env, v Value) {
	env.vars[r.name] = v
	env.globals[r.name] = v
}

func (r *varRef) value(env *Env) Value {
	return env.vars[r.name]
}

// indexRef 数组元素引用（&$arr[$k]）
type indexRef struct {
	arrExpr Expr
	key     Value
}

func (r *indexRef) assign(env *Env, v Value) {
	arr, err := env.evalExpr(r.arrExpr)
	if err != nil || arr.Kind != KindArray {
		return
	}
	arr.ArraySet(r.key, v)
	// 写回根变量（$arr[$k] 修改后需更新 $arr；基变量为别名时写穿到引用目标）
	if ve, ok := r.arrExpr.(*VarExpr); ok {
		env.writeBackRootVar(ve.Name, arr)
	}
}

func (r *indexRef) value(env *Env) Value {
	arr, err := env.evalExpr(r.arrExpr)
	if err != nil || arr.Kind != KindArray {
		return NewNull()
	}
	return arr.ArrayGet(r.key)
}

// propRef 对象属性引用（&$obj->prop）
type propRef struct {
	recvExpr Expr
	prop     string
}

func (r *propRef) assign(env *Env, v Value) {
	recv, err := env.evalExpr(r.recvExpr)
	if err != nil || recv.Kind != KindObject || recv.Object == nil {
		return
	}
	recv.Object.SetProp(r.prop, v)
}

func (r *propRef) value(env *Env) Value {
	recv, err := env.evalExpr(r.recvExpr)
	if err != nil || recv.Kind != KindObject || recv.Object == nil {
		return NewNull()
	}
	return recv.Object.Properties[r.prop]
}

// ---------------------------------------------------------------------------
// 用户函数 by-ref 形参：引用目标是"调用方持久作用域"（saved 快照，函数返回后即生效），
// 而不是函数体临时作用域（e.vars 会被整体还原，直接写它会被丢弃）。
// ---------------------------------------------------------------------------

// outerVarRef：指向调用方作用域中的某个变量（&$var 形参）
type outerVarRef struct {
	name    string
	outer   *map[string]Value
	globals *map[string]Value
}

func (r *outerVarRef) assign(env *Env, v Value) {
	(*r.outer)[r.name] = v
	if r.globals != nil {
		(*r.globals)[r.name] = v
	}
}

func (r *outerVarRef) value(env *Env) Value {
	return (*r.outer)[r.name]
}

// outerIndexRef：指向调用方作用域中数组的下标链（&$arr[$k][...] 形参）
type outerIndexRef struct {
	root    string
	outer   *map[string]Value
	globals *map[string]Value
	keys    []Value
}

func (r *outerIndexRef) assign(env *Env, v Value) {
	base := (*r.outer)[r.root]
	if base.Kind != KindArray {
		base = NewArray()
	}
	setNestedArray(&base, r.keys, v)
	(*r.outer)[r.root] = base
	if r.globals != nil {
		(*r.globals)[r.root] = base
	}
}

func (r *outerIndexRef) value(env *Env) Value {
	cur := (*r.outer)[r.root]
	for _, k := range r.keys {
		if cur.Kind == KindArray {
			cur = cur.ArrayGet(k)
		} else {
			return NewNull()
		}
	}
	return cur
}

// objPropRef：指向已捕获对象实例的属性（可带下标链）。对象按句柄共享，天然跨作用域持久。
type objPropRef struct {
	obj  *ObjectInstance
	prop string
	keys []Value
}

func (r *objPropRef) assign(env *Env, v Value) {
	if r.obj == nil {
		return
	}
	if len(r.keys) == 0 {
		r.obj.SetProp(r.prop, v)
		return
	}
	arr := r.obj.Properties[r.prop]
	if arr.Kind != KindArray {
		arr = NewArray()
	}
	setNestedArray(&arr, r.keys, v)
	r.obj.SetProp(r.prop, arr)
}

func (r *objPropRef) value(env *Env) Value {
	if r.obj == nil {
		return NewNull()
	}
	cur := r.obj.Properties[r.prop]
	for _, k := range r.keys {
		if cur.Kind == KindArray {
			cur = cur.ArrayGet(k)
		} else {
			return NewNull()
		}
	}
	return cur
}

// ---------------------------------------------------------------------------
// 入口与辅助
// ---------------------------------------------------------------------------

// ParseProgram 便捷入口：源码 -> 程序
func ParseProgram(src string) (*Program, error) {
	toks, err := NewLexer(src).Tokenize()
	if err != nil {
		return nil, err
	}
	return NewParser(toks).Parse()
}

// os_ReadFile 读本地文件
func os_ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
