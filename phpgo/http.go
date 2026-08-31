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
	"regexp"
	"sort"
	"strings"
	"time"

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
	// 写 header()
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
		// CURLOPT_ENCODING 显式设置时 Go 不会自动解压，按 Content-Encoding 手动解压
		if opts != nil && opts.Encoding != "" {
			switch strings.ToLower(resp.Header.Get("Content-Encoding")) {
			case "gzip":
				if gz, gerr := gzip.NewReader(bytes.NewReader(data)); gerr == nil {
					if d, derr := io.ReadAll(gz); derr == nil {
						data = d
					}
					_ = gz.Close()
				}
			case "deflate":
				if zr, zerr := zlib.NewReader(bytes.NewReader(data)); zerr == nil {
					if d, derr := io.ReadAll(zr); derr == nil {
						data = d
					}
					_ = zr.Close()
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

// ---------------------------------------------------------------------------
// 内置函数：PCRE（Go RE2 子集）
// ---------------------------------------------------------------------------

// phpPregMatch 实现 preg_match($pattern, $subject, &$matches)
func phpPregMatch(env *Env, vs []Value) (Value, error) {
	if len(vs) < 2 {
		return NewInt(0), nil
	}
	re, err := compilePHPRegex(vs[0].ToString())
	if err != nil {
		return NewInt(0), err
	}
	subj := vs[1].ToString()
	m := re.FindStringSubmatch(subj)
	if m == nil {
		return NewInt(0), nil
	}
	if len(vs) >= 3 {
		arr := NewArray()
		for i, s := range m {
			arr.ArraySet(NewInt(int64(i)), NewString(s))
		}
		writeRef(env, vs[2], arr)
	}
	return NewInt(1), nil
}

// phpPregMatchAll 实现 preg_match_all
func phpPregMatchAll(env *Env, vs []Value) (Value, error) {
	if len(vs) < 2 {
		return NewInt(0), nil
	}
	re, err := compilePHPRegex(vs[0].ToString())
	if err != nil {
		return NewInt(0), err
	}
	subj := vs[1].ToString()
	all := re.FindAllStringSubmatch(subj, -1)
	if len(vs) >= 3 {
		outer := NewArray()
		for i, m := range all {
			inner := NewArray()
			for j, s := range m {
				inner.ArraySet(NewInt(int64(j)), NewString(s))
			}
			outer.ArraySet(NewInt(int64(i)), inner)
		}
		writeRef(env, vs[2], outer)
	}
	return NewInt(int64(len(all))), nil
}

// phpPregReplace 实现 preg_replace($pattern, $repl, $subject[, $limit])
// 支持 fj.php 那种 lookbehind 特例（Go RE2 不支持 (?<=)，用字符串级 workaround）。
// 支持 bjydott.php 那种纯 lookahead 特例（Go RE2 不支持 (?=)，用字符串级 workaround）。
func phpPregReplace(env *Env, vs []Value) (Value, error) {
	if len(vs) < 3 {
		return NewNull(), nil
	}
	pattern := vs[0].ToString()
	repl := vs[1].ToString()
	subj := vs[2].ToString()
	// lookbehind 特例：(?<=\/)[^\/.]+(?=\.m3u8)
	if strings.Contains(pattern, "(?<=") {
		return NewString(lookbehindWorkaround(pattern, repl, subj)), nil
	}
	// lookahead 特例：/zoneoffset.*?(?=accountinfo)/
	// Go RE2 不支持 (?=...)，需要用 lookaheadWorkaround 处理
	if strings.Contains(pattern, "(?=") {
		return NewString(lookaheadWorkaround(pattern, repl, subj)), nil
	}
	re, err := compilePHPRegex(pattern)
	if err != nil {
		return NewString(subj), err
	}
	return NewString(re.ReplaceAllString(subj, repl)), nil
}

// writeRef 若第 i 个参数是引用（&$var），把 v 写回该变量
func writeRef(env *Env, refVal Value, v Value) {
	if refVal.Kind == KindRef && refVal.Ref != nil {
		refVal.Ref.assign(env, v)
	}
}

// lookbehindWorkaround 处理 (?<=X)MAIN(?=Y) 形式的简单后行/先行断言：
// Go RE2 不支持 lookbehind (?<=)，所以把断言改写为捕获组：
//
//	原 pattern = [prefix] (?<=LIT) MAIN (?=TAIL) [suffix]
//	改写为     = [prefix] (LIT) (MAIN) (TAIL) [suffix]
//
// 匹配后只替换 MAIN 部分（第 2 个捕获组），保留前后缀。
// 这是针对 fj.php 等场景的 workaround。
func lookbehindWorkaround(pattern, repl, subj string) string {
	p := pattern

	// 0. 先剥离 PCRE 定界符（如 /.../、#...#、~...~、!...!），并去掉末尾修饰符
	if len(p) >= 2 {
		if d := p[0]; (d == '/' || d == '#' || d == '~' || d == '!') && p[len(p)-1] == d {
			inner := p[1 : len(p)-1]
			// 去掉结尾修饰符（如 /.../i）
			if i := strings.LastIndexByte(inner, d); i >= 0 {
				// 仅当斜杠出现且非组结构时视为结束；保守处理：截取首定界符到末定界符
			}
			p = inner
		}
	}

	// 1. 提取 lookbehind 字面量并从 pattern 中移除断言组
	lit := ""
	if idx := strings.Index(p, "(?<="); idx >= 0 {
		rest := p[idx+4:] // 跳过 "(?<="
		end := strings.Index(rest, ")")
		if end < 0 {
			return subj
		}
		lit = rest[:end]              // 断言内的字面量（可能含转义 \/）
		p = p[:idx] + p[idx+4+end+1:] // 移除 (?<=...) 整个组
	}

	// 2. 提取 lookahead 字面量并从 pattern 中移除断言组
	tail := ""
	if idx := strings.Index(p, "(?="); idx >= 0 {
		rest := p[idx+3:] // 跳过 "(?="
		end := strings.Index(rest, ")")
		if end < 0 {
			return subj
		}
		tail = rest[:end]             // 断言内的字面量
		p = p[:idx] + p[idx+3+end+1:] // 移除 (?=...) 整个组
	}

	// 3. 清理 lit/tail 中的正则转义反斜杠（如 \/ -> /、\. -> .），
	//    因为我们要用 QuoteMeta 做字面量捕获匹配
	litClean := unescapeRegexLit(lit)
	tailClean := unescapeRegexLit(tail)

	// 4. 编译 (LIT)(MAIN)(TAIL)，替换时保留 lit 与 tail，只换 MAIN
	full := "(" + regexp.QuoteMeta(litClean) + ")(" + p + ")(" + regexp.QuoteMeta(tailClean) + ")"
	re, err := regexp.Compile(full)
	if err != nil {
		return subj
	}
	return re.ReplaceAllString(subj, "${1}"+repl+"${3}")
}

// lookaheadWorkaround 处理 MAIN(?=TAIL) 形式的纯先行断言：
// Go RE2 不支持 (?=...) lookahead，所以把断言改写为捕获组：
//
//	原 pattern = [prefix] MAIN (?=TAIL) [suffix]
//	改写为     = [prefix] (MAIN) (TAIL) [suffix]
//
// 匹配后只替换 MAIN 部分（第 1 个捕获组），保留 TAIL（第 2 个捕获组）。
// 例如 /zoneoffset.*?(?=accountinfo)/ → (zoneoffset.*?)(accountinfo)
// 替换时用 ${2} 保留 TAIL 部分。
func lookaheadWorkaround(pattern, repl, subj string) string {
	p := pattern

	// 0. 剥离 PCRE 定界符（如 /.../、#...#、~...~、!...!）
	if len(p) >= 2 {
		if d := p[0]; d == '/' || d == '#' || d == '~' || d == '!' {
			inner := p[1:]
			if i := strings.LastIndexByte(inner, d); i >= 0 {
				p = inner[:i]
			}
		}
	}

	// 1. 提取 lookahead 字面量并从 pattern 中移除断言组
	tail := ""
	if idx := strings.Index(p, "(?="); idx >= 0 {
		rest := p[idx+3:] // 跳过 "(?="
		end := strings.Index(rest, ")")
		if end < 0 {
			return subj
		}
		tail = rest[:end]
		p = p[:idx] + p[idx+3+end+1:] // 移除 (?=...) 整个组
	}

	// 2. tail 是正则模式（lookahead 内容本身就是正则），直接使用
	//    但要清理掉 PCRE 转义的反斜杠（\/ -> / 等）以兼容 RE2
	tailClean := unescapeRegexLit(tail)

	// 3. 编译 (MAIN)(TAIL)，替换时保留 TAIL，只换 MAIN
	full := "(" + p + ")(" + tailClean + ")"
	re, err := regexp.Compile(full)
	if err != nil {
		return subj
	}
	return re.ReplaceAllString(subj, repl+"${2}")
}

// unescapeRegexLit 去除正则字面量中的转义反斜杠（\. -> .、\/ -> / 等），
// 用于 lookbehind/lookahead 断言内的字面量还原。
func unescapeRegexLit(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++ // 跳过反斜杠，保留其后字符
			b.WriteByte(s[i])
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
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

// compilePHPRegex 把 PHP PCRE 表达式转为 Go RE2。
// 处理定界符（//、##、~~ 等），并放行 RE2 支持的 (?= lookahead)。
// lookbehind (?<= 在 preg_replace 路径已特例处理，这里返回原样交由调用方判断。
func compilePHPRegex(pat string) (*regexp.Regexp, error) {
	// 去定界符：首字符为定界符，尾字符为同一定界符，可选修饰符
	mods := ""
	if len(pat) >= 2 {
		delim := pat[0]
		if delim == '/' || delim == '#' || delim == '~' || delim == '!' {
			inner := pat[1:]
			// 找最后一个定界符的位置（尾部可能有修饰符如 /pattern/i）
			if i := strings.LastIndexByte(inner, delim); i >= 0 {
				mods = inner[i+1:] // 提取修饰符
				inner = inner[:i]  // 只保留定界符之间的部分
			}
			pat = inner
		}
	}
	// 构建 Go 内联修饰符前缀
	// PCRE 修饰符 → Go RE2 内联标志:
	// i → (?i) 不区分大小写
	// m → (?m) 多行模式 (^/$ 匹配每行)
	// s → (?s) . 匹配换行符
	// x → (?x) 忽略空白和 # 注释
	prefix := ""
	for _, ch := range mods {
		switch ch {
		case 'i', 's', 'm', 'x':
			prefix += string(ch)
		}
	}
	if prefix != "" {
		pat = "(?" + prefix + ")" + pat
	}
	// 无修饰符时不添加前缀，与 PHP PCRE 默认行为一致（区分大小写）
	re, err := regexp.Compile(pat)
	if err != nil {
		// 失败则尝试去掉内联修饰符前缀
		if prefix != "" {
			return regexp.Compile(strings.TrimPrefix(pat, "(?"+prefix+")"))
		}
		return regexp.Compile(pat)
	}
	return re, nil
}
