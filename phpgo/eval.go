package phpgo

import (
	"fmt"
	"io"
	mathrand "math/rand"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ProxyFunc 是 Go 实现的 HTTP 请求能力（替代 curl + 代理）。
type ProxyFunc func(method, url string, opts *CurlOptions) (*ProxyResult, error)

// ProxyResult 是 proxy 请求的结果
type ProxyResult struct {
	Body         string
	StatusCode   int
	Location     string // 重定向 URL（如果有）
	ContentType  string
	EffectiveURL string   // 最终 URL（跟随重定向后）
	Headers      []string // 响应头（"Key: Value" 形式，供 get_headers 使用）
	PrimaryIP    string   // 实际连接的对端 IP（CURLINFO_PRIMARY_IP）
}

// CurlOptions 对应 PHP curl_setopt 的关键选项（已注册的 CURLOPT_* 常量均在此严格生效）
type CurlOptions struct {
	Proxy               string // CURLOPT_PROXY
	ProxyType           string // CURLOPT_PROXYTYPE
	Headers             []string
	PostData            string
	HasPostData         bool    // 是否设置了 CURLOPT_POSTFIELDS
	Timeout             int     // CURLOPT_TIMEOUT（整数秒，兼容旧逻辑）
	TimeoutFloat        float64 // CURLOPT_TIMEOUT（浮点秒，优先于 Timeout）
	ConnectTimeoutFloat float64 // CURLOPT_CONNECTTIMEOUT（浮点秒）
	UserAgent           string
	Method              string
	FollowRedirect      bool   // CURLOPT_FOLLOWLOCATION
	MaxRedirects        int    // CURLOPT_MAXREDIRS（>0 时限制重定向次数）
	SkipSSL             bool   // CURLOPT_SSL_VERIFYPEER=false
	SkipHostVerify      bool   // CURLOPT_SSL_VERIFYHOST=0（跳过主机名校验）
	TLSVersion          uint16 // CURLOPT_SSLVERSION（映射到 tls.VersionTLSxx）
	CAFile              string // CURLOPT_CAINFO
	CAPath              string // CURLOPT_CAPATH
	CertFile            string // CURLOPT_SSLCERT
	KeyFile             string // CURLOPT_SSLKEY
	Referer             string // CURLOPT_REFERER
	Cookie              string // CURLOPT_COOKIE
	CookieFile          string // CURLOPT_COOKIEFILE（从文件读 Cookie）
	CookieJar           string // CURLOPT_COOKIEJAR（把 Set-Cookie 写入文件）
	Encoding            string // CURLOPT_ENCODING（Accept-Encoding，""=由 Go 自动 gzip）
	Port                int    // CURLOPT_PORT（覆盖 URL 端口）
	FailOnError         bool   // CURLOPT_FAILONERROR（HTTP>=400 视为错误）
	IncludeHeader       bool   // CURLOPT_HEADER（输出含响应头）
	ForbidReuse         bool   // CURLOPT_FORBID_REUSE（Connection: close）
	Verbose             bool   // CURLOPT_VERBOSE（打印请求信息）
	HTTPGet             bool   // CURLOPT_HTTPGET（强制 GET）
	WriteFunc           Value  // CURLOPT_WRITEFUNCTION 回调
	HeaderFunc          Value  // CURLOPT_HEADERFUNCTION 回调
	IPResolve           int    // CURLOPT_IPRESOLVE（0=whatever,1=V4,2=V6）
}

// Env 执行环境
type Env struct {
	vars          map[string]Value
	funcs         map[string]*FuncDecl
	classes       map[string]*ClassDecl
	globals       map[string]Value // $GLOBALS
	consts        map[string]Value
	server        map[string]string // $_SERVER
	get           map[string]string // $_GET
	post          map[string]string // $_POST
	cookie        map[string]string // $_COOKIE
	session       map[string]string // $_SESSION
	envmap        map[string]string // $_ENV
	ufiles        map[string]Value  // $_FILES
	reqURI        string            // $_SERVER["REQUEST_URI"]
	phpInput      string            // php://input 原始请求体
	headers       []string          // 捕获的 header() 输出
	statusCode    int               // 显式设置的状态码（如 header("HTTP/1.1 404")）
	statusCodeSet bool              // 是否显式设置过状态码
	exitLoc       bool              // 触发 exit
	exitVal       Value             // exit() 参数值
	proxy         ProxyFunc
	echoOut       *strings.Builder
	obStack       []*strings.Builder         // output buffer stack
	breakN        int                        // 待处理的 break 层数
	continueN     int                        // 待处理的 continue 层数
	files         map[int]io.ReadWriteCloser // 文件/流句柄表（fd -> 资源）
	nextFd        int                        // 下一个可用 fd
	scriptPath    string                     // 当前脚本路径（用于 __DIR__/__FILE__）
	loc           *time.Location             // 当前请求默认时区（date/strtotime 使用）

	// 占位函数真实化所需的运行时状态
	rng           *mathrand.Rand    // 可播种 PRNG（srand/mt_srand 控制，未显式播种时自动随机）
	ini           map[string]string // ini_set/ini_get 存储
	errorLevel    int64             // error_reporting 级别
	jsonErr       int               // 最近一次 JSON 错误码（json_last_error）
	jsonErrMsg    string            // 最近一次 JSON 错误信息（json_last_error_msg）
	sessionID     string            // 会话 ID（session_id/session_start）
	implicitFlush bool              // ob_implicit_flush 标记
}

// NewEnv 创建执行环境
func NewEnv(proxy ProxyFunc) *Env {
	ev := &Env{
		vars:    map[string]Value{},
		funcs:   map[string]*FuncDecl{},
		classes: map[string]*ClassDecl{},
		globals: map[string]Value{},
		consts:  defaultPHPConsts(),
		server:  map[string]string{},
		get:     map[string]string{},
		post:    map[string]string{},
		cookie:  map[string]string{},
		session: map[string]string{},
		envmap:  map[string]string{},
		ufiles:  map[string]Value{},
		ini:     map[string]string{},
		proxy:   proxy,
		echoOut: &strings.Builder{},
		files:   map[int]io.ReadWriteCloser{},
		nextFd:  1,
		loc:     currentPHPLocation(), // 播种为配置默认时区（date_default_timezone_set 可改）
	}
	return ev
}

// defaultPHPConsts 返回 PHP 预定义常量
func defaultPHPConsts() map[string]Value {
	c := map[string]Value{}
	// JSON
	c["JSON_UNESCAPED_UNICODE"] = NewInt(256)
	c["JSON_UNESCAPED_SLASHES"] = NewInt(64)
	c["JSON_PRETTY_PRINT"] = NewInt(128)
	c["JSON_THROW_ON_ERROR"] = NewInt(4194304)
	c["JSON_HEX_TAG"] = NewInt(1)
	c["JSON_HEX_AMP"] = NewInt(2)
	c["JSON_HEX_APOS"] = NewInt(4)
	c["JSON_HEX_QUOT"] = NewInt(8)
	c["JSON_FORCE_OBJECT"] = NewInt(16)
	c["JSON_NUMERIC_CHECK"] = NewInt(32)
	c["JSON_BIGINT_AS_STRING"] = NewInt(2)
	c["JSON_UNESCAPED_LINE_TERMINATORS"] = NewInt(2048)
	// CURL
	c["CURLOPT_URL"] = NewInt(1)
	c["CURLOPT_RETURNTRANSFER"] = NewInt(19913)
	c["CURLOPT_POST"] = NewInt(47)
	c["CURLOPT_POSTFIELDS"] = NewInt(10015)
	c["CURLOPT_HTTPHEADER"] = NewInt(10023)
	c["CURLOPT_TIMEOUT"] = NewInt(13)
	c["CURLOPT_CONNECTTIMEOUT"] = NewInt(78)
	c["CURLOPT_USERAGENT"] = NewInt(10018)
	c["CURLOPT_FOLLOWLOCATION"] = NewInt(52)
	c["CURLOPT_CUSTOMREQUEST"] = NewInt(10036)
	c["CURLOPT_NOBODY"] = NewInt(44)
	c["CURLOPT_PROXY"] = NewInt(10004)
	c["CURLOPT_PROXYTYPE"] = NewInt(10100)
	c["CURLOPT_SSL_VERIFYPEER"] = NewInt(64)
	c["CURLOPT_SSL_VERIFYHOST"] = NewInt(81)
	c["CURLOPT_ENCODING"] = NewInt(10102)
	c["CURLOPT_REFERER"] = NewInt(10016)
	c["CURLOPT_COOKIE"] = NewInt(10022)
	c["CURLOPT_COOKIEFILE"] = NewInt(10031)
	c["CURLOPT_COOKIEJAR"] = NewInt(10082)
	c["CURLOPT_HEADER"] = NewInt(42)
	c["CURLOPT_VERBOSE"] = NewInt(41)
	c["CURLOPT_FAILONERROR"] = NewInt(45)
	c["CURLOPT_FORBID_REUSE"] = NewInt(75)
	c["CURLOPT_FRESH_CONNECT"] = NewInt(74)
	c["CURLOPT_MAXREDIRS"] = NewInt(68)
	c["CURLOPT_SSLVERSION"] = NewInt(32)
	c["CURLOPT_CAINFO"] = NewInt(10065)
	c["CURLOPT_CAPATH"] = NewInt(10097)
	c["CURLOPT_SSLCERT"] = NewInt(10025)
	c["CURLOPT_SSLKEY"] = NewInt(10026)
	c["CURLOPT_HTTPGET"] = NewInt(80)
	c["CURLOPT_PORT"] = NewInt(3)
	c["CURLOPT_FILE"] = NewInt(10001)
	c["CURLOPT_WRITEFUNCTION"] = NewInt(20011)
	c["CURLOPT_HEADERFUNCTION"] = NewInt(20079)
	c["CURLOPT_IPRESOLVE"] = NewInt(113)
	c["CURL_IPRESOLVE_WHATEVER"] = NewInt(0)
	c["CURL_IPRESOLVE_V4"] = NewInt(1)
	c["CURL_IPRESOLVE_V6"] = NewInt(2)
	// CURLINFO
	c["CURLINFO_HTTP_CODE"] = NewInt(2097154)
	c["CURLINFO_RESPONSE_CODE"] = NewInt(2097154)
	c["CURLINFO_EFFECTIVE_URL"] = NewInt(1048577)
	c["CURLINFO_CONTENT_TYPE"] = NewInt(1048593)
	c["CURLINFO_TOTAL_TIME"] = NewInt(3145731)
	c["CURLINFO_URL"] = NewInt(1048577)
	c["CURLINFO_REDIRECT_URL"] = NewInt(3145744)
	c["CURLINFO_PRIMARY_IP"] = NewInt(1769476)
	// DNS（dns_get_record 类型常量，PHP 语义）
	c["DNS_A"] = NewInt(1)
	c["DNS_NS"] = NewInt(2)
	c["DNS_CNAME"] = NewInt(16)
	c["DNS_SOA"] = NewInt(32)
	c["DNS_PTR"] = NewInt(64)
	c["DNS_HINFO"] = NewInt(128)
	c["DNS_MX"] = NewInt(16384)
	c["DNS_TXT"] = NewInt(32768)
	c["DNS_AAAA"] = NewInt(134217728)
	c["DNS_SRV"] = NewInt(33554432)
	c["DNS_NAPTR"] = NewInt(67108864)
	c["DNS_A6"] = NewInt(16777216)
	c["DNS_ALL"] = NewInt(251721779)
	c["DNS_ANY"] = NewInt(268435456)
	// PHP 常量
	c["PHP_EOL"] = NewString("\n")
	c["PHP_INT_MAX"] = NewInt(9223372036854775807)
	c["PHP_INT_MIN"] = NewInt(-9223372036854775808)
	c["DIRECTORY_SEPARATOR"] = NewString("/")
	c["PATH_SEPARATOR"] = NewString(":")
	// SORT
	c["SORT_ASC"] = NewInt(4)
	c["SORT_DESC"] = NewInt(3)
	c["SORT_REGULAR"] = NewInt(0)
	c["SORT_NUMERIC"] = NewInt(1)
	c["SORT_STRING"] = NewInt(2)
	// PHP_QUERY
	c["PHP_QUERY_RFC1738"] = NewInt(1)
	c["PHP_QUERY_RFC3986"] = NewInt(2)
	// PHP_URL_* 常量（parse_url 的 component 参数）
	c["PHP_URL_SCHEME"] = NewInt(0)
	c["PHP_URL_HOST"] = NewInt(1)
	c["PHP_URL_PORT"] = NewInt(2)
	c["PHP_URL_USER"] = NewInt(3)
	c["PHP_URL_PASS"] = NewInt(4)
	c["PHP_URL_PATH"] = NewInt(5)
	c["PHP_URL_QUERY"] = NewInt(6)
	c["PHP_URL_FRAGMENT"] = NewInt(7)
	// STR_PAD
	c["STR_PAD_LEFT"] = NewInt(0)
	c["STR_PAD_RIGHT"] = NewInt(1)
	c["STR_PAD_BOTH"] = NewInt(2)
	// JSON 常量别名
	c["JSON_ERROR_NONE"] = NewInt(0)
	// COUNT
	c["COUNT_NORMAL"] = NewInt(0)
	c["COUNT_RECURSIVE"] = NewInt(1)
	// FILE
	c["FILE_APPEND"] = NewInt(8)
	c["FILE_USE_INCLUDE_PATH"] = NewInt(1)
	c["LOCK_EX"] = NewInt(2)
	c["LOCK_SH"] = NewInt(1)
	c["LOCK_UN"] = NewInt(3)
	c["LOCK_NB"] = NewInt(4)
	// MCRYPT / OPENSSL
	c["OPENSSL_RAW_DATA"] = NewInt(1)
	c["OPENSSL_ZERO_PADDING"] = NewInt(3)
	c["OPENSSL_DONT_ZERO_PAD_KEY"] = NewInt(4)
	// MATH
	c["M_PI"] = NewFloat(3.141592653589793)
	c["PHP_FLOAT_MAX"] = NewFloat(1.7976931348623157e+308)
	c["PHP_FLOAT_MIN"] = NewFloat(2.2250738585072014e-308)
	// E_* error levels
	c["E_ERROR"] = NewInt(1)
	c["E_WARNING"] = NewInt(2)
	c["E_NOTICE"] = NewInt(8)
	c["E_CORE_ERROR"] = NewInt(16)
	c["E_CORE_WARNING"] = NewInt(32)
	c["E_ALL"] = NewInt(32767)
	c["E_STRICT"] = NewInt(2048)
	c["E_DEPRECATED"] = NewInt(8192)
	c["E_USER_ERROR"] = NewInt(256)
	c["E_USER_WARNING"] = NewInt(512)
	c["E_USER_NOTICE"] = NewInt(1024)
	c["E_USER_DEPRECATED"] = NewInt(16384)
	return c
}

// SetGet 设置 $_GET
func (e *Env) SetGet(k, v string) { e.get[k] = v }

// SetPost 设置 $_POST
func (e *Env) SetPost(k, v string) { e.post[k] = v }

// SetCookie 设置 $_COOKIE
func (e *Env) SetCookie(k, v string) { e.cookie[k] = v }

// SetSession 设置 $_SESSION
func (e *Env) SetSession(k, v string) { e.session[k] = v }

// SetEnv 设置 $_ENV
func (e *Env) SetEnv(k, v string) { e.envmap[k] = v }

// SetFile 设置 $_FILES 条目（结构化数组：name/size/tmp_name/type/error）
func (e *Env) SetFile(field, key, val string) {
	if _, ok := e.ufiles[field]; !ok {
		e.ufiles[field] = NewArray()
	}
	arr := e.ufiles[field]
	arr.ArraySet(NewString(key), NewString(val))
	e.ufiles[field] = arr
}

// SetServer 设置 $_SERVER
func (e *Env) SetServer(k, v string) { e.server[k] = v }

// SetRequestURI 设置请求 URI
func (e *Env) SetRequestURI(u string) { e.reqURI = u; e.server["REQUEST_URI"] = u }

// SetPHPInput 设置 php://input 原始请求体
func (e *Env) SetPHPInput(body string) { e.phpInput = body }

// SetScriptPath 设置当前脚本路径（用于 __DIR__/__FILE__）
func (e *Env) SetScriptPath(p string) { e.scriptPath = p }

// ScriptDir 返回当前脚本所在目录
func (e *Env) ScriptDir() string {
	if e.scriptPath != "" {
		return filepath.Dir(e.scriptPath)
	}
	return "."
}

// ResolvePath 将相对路径解析为相对于脚本目录的路径（对齐 PHP 行为）
func (e *Env) ResolvePath(p string) string {
	if p == "" {
		return p
	}
	// php:// input 不处理
	if strings.HasPrefix(p, "php://") {
		return p
	}
	// http(s):// URL 不处理
	if strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") {
		return p
	}
	// 绝对路径不处理
	if filepath.IsAbs(p) {
		return p
	}
	// 相对路径：相对于脚本目录
	return filepath.Join(e.ScriptDir(), p)
}

// ExitValue 返回 exit() 的参数值
func (e *Env) ExitValue() Value { return e.exitVal }

// Run 执行程序
func (e *Env) Run(prog *Program) (Value, error) {
	// PHP 函数声明提升：先注册所有函数定义
	for _, st := range prog.Stmts {
		if fn, ok := st.(*FuncDecl); ok {
			e.funcs[fn.Name] = fn
		}
	}
	for _, st := range prog.Stmts {
		if e.exitLoc {
			break
		}
		if e.breakN > 0 || e.continueN > 0 {
			break
		}
		if _, err := e.execStmt(st); err != nil {
			return NewNull(), err
		}
	}
	// 脚本结束：把残留的输出缓冲逐层刷到最终输出（对齐 PHP 隐式 ob_end_flush）
	for len(e.obStack) > 0 {
		e.echoOut.WriteString(e.obStack[len(e.obStack)-1].String())
		e.obStack = e.obStack[:len(e.obStack)-1]
	}
	return NewNull(), nil
}

// controlFlow 控制流信号
type controlFlow int

const (
	cfNormal controlFlow = iota
	cfReturn
	cfBreak
	cfContinue
)

type execResult struct {
	val   Value
	flow  controlFlow
	count int // break/continue 的层数
}

func (e *Env) execStmt(st Stmt) (execResult, error) {
	switch s := st.(type) {
	case *FuncDecl:
		e.funcs[s.Name] = s
		return execResult{}, nil
	case *ExprStmt:
		_, err := e.evalExpr(s.E)
		return execResult{}, err
	case *EchoStmt:
		v, err := e.evalExpr(s.E)
		if err != nil {
			return execResult{}, err
		}
		e.writeOutput(v.ToString())
		return execResult{}, nil
	case *ExitStmt:
		e.exitLoc = true
		if s.E != nil {
			v, err := e.evalExpr(s.E)
			if err != nil {
				return execResult{}, err
			}
			e.exitVal = v
			// die()/exit() 带参数时输出参数（PHP 行为）
			if v.Kind == KindInt {
				// 整数参数作为退出码，不输出
			} else {
				e.writeOutput(v.ToString())
			}
		}
		return execResult{flow: cfReturn}, nil
	case *UnsetStmt:
		for _, arg := range s.Args {
			switch a := arg.(type) {
			case *VarExpr:
				delete(e.vars, a.Name)
				delete(e.globals, a.Name)
			case *IndexExpr:
				// 先获取数组变量名，获取引用，unset 后写回
				base, err := e.evalExpr(a.Arr)
				if err != nil || base.Kind != KindArray {
					continue
				}
				kv, err := e.evalExpr(a.Key)
				if err != nil {
					continue
				}
				base.ArrayUnset(kv)
				// 写回根变量（$arr[$key] unset 后需更新 $arr）
				if v, ok := a.Arr.(*VarExpr); ok {
					e.vars[v.Name] = base
					e.globals[v.Name] = base
				}
			}
		}
		return execResult{}, nil
	case *ReturnStmt:
		if s.E == nil {
			return execResult{val: NewNull(), flow: cfReturn}, nil
		}
		v, err := e.evalExpr(s.E)
		return execResult{val: v, flow: cfReturn}, err
	case *AssignStmt:
		val, err := e.evalExpr(s.Value)
		if err != nil {
			return execResult{}, err
		}
		// PHP 数组赋值是按值拷贝
		val = val.Clone()
		// 属性或数组元素赋值：$this->prop = val, $arr[$k] = val
		if s.Target != nil {
			return e.evalAssignTarget(s.Target, val, s.Concat, s.Op)
		}
		if s.Concat {
			old := e.vars[s.Name]
			val = Value{Kind: KindString, Str: old.ToString() + val.ToString()}
		} else if s.Op != "" {
			old := e.vars[s.Name]
			switch s.Op {
			case "+=":
				if old.Kind == KindString || val.Kind == KindString {
					val = NewString(old.ToString() + val.ToString())
				} else {
					val = NewInt(old.ToInt() + val.ToInt())
				}
			case "-=":
				val = NewInt(old.ToInt() - val.ToInt())
			case "*=":
				val = NewInt(old.ToInt() * val.ToInt())
			case "/=":
				if val.ToInt() != 0 {
					val = NewInt(old.ToInt() / val.ToInt())
				} else {
					val = NewInt(0)
				}
			case "^=":
				val = NewInt(old.ToInt() ^ val.ToInt())
			}
		}
		e.vars[s.Name] = val
		e.globals[s.Name] = val
		return execResult{val: val}, nil
	case *ArrayPushStmt:
		arr, err := e.evalExpr(s.Arr)
		if err != nil {
			return execResult{}, err
		}
		val, err := e.evalExpr(s.Val)
		if err != nil {
			return execResult{}, err
		}
		arr.ArraySet(NewInt(int64(len(arr.Keys))), val)
		// 回写
		if v, ok := s.Arr.(*VarExpr); ok {
			e.vars[v.Name] = arr
			e.globals[v.Name] = arr
		}
		return execResult{val: val}, nil
	case *ArrayAssignStmt:
		arr, err := e.evalExpr(s.Arr)
		if err != nil {
			return execResult{}, err
		}
		key, err := e.evalExpr(s.Key)
		if err != nil {
			return execResult{}, err
		}
		val, err := e.evalExpr(s.Val)
		if err != nil {
			return execResult{}, err
		}
		arr.ArraySet(key, val)
		// 回写根变量
		if v, ok := s.Arr.(*VarExpr); ok {
			e.vars[v.Name] = arr
			e.globals[v.Name] = arr
		}
		return execResult{val: val}, nil
	case *NestedArrayAssignStmt:
		return e.execNestedArrayAssign(s)
	case *IfStmt:
		for i, cond := range s.Conds {
			cv, err := e.evalExpr(cond)
			if err != nil {
				return execResult{}, err
			}
			if cv.ToBool() {
				return e.execBlock(s.Bodies[i])
			}
		}
		if s.Else != nil {
			return e.execBlock(s.Else)
		}
		return execResult{}, nil
	case *ForeachStmt:
		return e.execForeach(s)
	case *ForStmt:
		return e.execFor(s)
	case *WhileStmt:
		return e.execWhile(s)
	case *DoWhileStmt:
		return e.execDoWhile(s)
	case *SwitchStmt:
		return e.execSwitch(s)
	case *BreakStmt:
		return execResult{flow: cfBreak, count: s.N}, nil
	case *ContinueStmt:
		return execResult{flow: cfContinue, count: s.N}, nil
	case *GlobalStmt:
		for _, name := range s.Names {
			if _, ok := e.globals[name]; !ok {
				e.globals[name] = NewNull()
			}
			e.vars[name] = e.globals[name]
		}
		return execResult{}, nil
	case *ConstStmt:
		v, err := e.evalExpr(s.Val)
		if err != nil {
			return execResult{}, err
		}
		e.consts[s.Name] = v
		return execResult{}, nil
	case *ClassDecl:
		e.classes[s.Name] = s
		// 类常量注册到全局 consts（以 ClassName::CONSTNAME 格式）
		for _, c := range s.Consts {
			cv, err := e.evalExpr(c.Val)
			if err != nil {
				return execResult{}, err
			}
			e.consts[s.Name+"::"+c.Name] = cv
		}
		return execResult{}, nil
	case *PostIncStmt:
		old, ok := e.vars[s.Name]
		if !ok {
			old = NewNull()
		}
		var nv Value
		if s.IsDec {
			nv = NewInt(old.ToInt() - 1)
		} else {
			nv = NewInt(old.ToInt() + 1)
		}
		e.vars[s.Name] = nv
		e.globals[s.Name] = nv
		return execResult{val: old}, nil
	case *TryStmt:
		return e.execTry(s)
	case *ThrowStmt:
		v, err := e.evalExpr(s.E)
		if err != nil {
			return execResult{}, err
		}
		// 如果是 new Exception/RuntimeException 的结果，提取 message 和 class
		if v.Kind == KindArray {
			msgVal := v.ArrayGet(NewString("message"))
			classVal := v.ArrayGet(NewString("class"))
			return execResult{}, &PHPException{Msg: msgVal.ToString(), Class: classVal.ToString()}
		}
		return execResult{}, &PHPException{Msg: v.ToString(), Class: "RuntimeException"}
	}
	return execResult{}, nil
}

func (e *Env) execBlock(stmts []Stmt) (execResult, error) {
	for _, st := range stmts {
		if e.exitLoc {
			break
		}
		r, err := e.execStmt(st)
		if err != nil {
			return r, err
		}
		if r.flow != cfNormal {
			return r, nil
		}
	}
	return execResult{}, nil
}

// execNestedArrayAssign 处理 $arr[$k1][$k2]... = val
func (e *Env) execNestedArrayAssign(s *NestedArrayAssignStmt) (execResult, error) {
	// 取根变量
	var root Value
	if v, ok := s.Base.(*VarExpr); ok {
		root = e.vars[v.Name]
		if root.Kind != KindArray {
			root = NewArray()
		}
	}
	// 解析所有下标
	keys := make([]Value, len(s.Indices))
	for i, idxExpr := range s.Indices {
		if idxExpr == nil {
			// 追加模式（下标为空）
			// 在最终赋值时处理，这里先标记
			keys[i] = NewNull() // 标记为追加
		} else {
			k, err := e.evalExpr(idxExpr)
			if err != nil {
				return execResult{}, err
			}
			keys[i] = k
		}
	}
	// 赋值
	val, err := e.evalExpr(s.Val)
	if err != nil {
		return execResult{}, err
	}
	// 递归设置
	setNestedArray(&root, keys, val)
	// 回写根变量
	if v, ok := s.Base.(*VarExpr); ok {
		e.vars[v.Name] = root
		e.globals[v.Name] = root
	}
	return execResult{}, nil
}

// setNestedArray 递归地在数组中设置嵌套值
func setNestedArray(arr *Value, keys []Value, val Value) {
	if len(keys) == 1 {
		// 最后一级
		if keys[0].Kind == KindNull {
			// 追加模式
			arr.ArraySet(NewInt(int64(len(arr.Keys))), val)
		} else {
			arr.ArraySet(keys[0], val)
		}
		return
	}
	// 中间级：获取或创建子数组
	var key Value
	if keys[0].Kind == KindNull {
		key = NewInt(int64(len(arr.Keys)))
	} else {
		key = keys[0]
	}
	child := arr.ArrayGet(key)
	if child.Kind != KindArray {
		child = NewArray()
	}
	setNestedArray(&child, keys[1:], val)
	// 回写子数组到父数组
	arr.ArraySet(key, child)
}

// evalAssignTarget 处理对属性或数组元素的赋值
func (e *Env) evalAssignTarget(target Expr, val Value, concat bool, op string) (execResult, error) {
	switch t := target.(type) {
	case *PropertyAccess:
		recv, err := e.evalExpr(t.Receiver)
		if err != nil {
			return execResult{}, err
		}
		if recv.Kind == KindObject {
			if concat {
				old := recv.Object.Properties[t.Prop]
				val = NewString(old.ToString() + val.ToString())
			} else if op != "" {
				old := recv.Object.Properties[t.Prop]
				switch op {
				case "+=":
					if old.Kind == KindString || val.Kind == KindString {
						val = NewString(old.ToString() + val.ToString())
					} else {
						val = NewInt(old.ToInt() + val.ToInt())
					}
				case "-=":
					val = NewInt(old.ToInt() - val.ToInt())
				case "*=":
					val = NewInt(old.ToInt() * val.ToInt())
				case "/=":
					if val.ToInt() != 0 {
						val = NewInt(old.ToInt() / val.ToInt())
					} else {
						val = NewInt(0)
					}
				case "^=":
					val = NewInt(old.ToInt() ^ val.ToInt())
				}
			}
			recv.Object.Properties[t.Prop] = val
			return execResult{val: val}, nil
		}
		// 非对象（数组模拟）回退
		recv.ArraySet(NewString(t.Prop), val)
		return execResult{val: val}, nil
	case *IndexExpr:
		// $arr[$key] = val
		arr, err := e.evalExpr(t.Arr)
		if err != nil {
			return execResult{}, err
		}
		key, err := e.evalExpr(t.Key)
		if err != nil {
			return execResult{}, err
		}
		if concat {
			old := arr.ArrayGet(key)
			val = NewString(old.ToString() + val.ToString())
		} else if op != "" {
			old := arr.ArrayGet(key)
			switch op {
			case "+=":
				if old.Kind == KindString || val.Kind == KindString {
					val = NewString(old.ToString() + val.ToString())
				} else {
					val = NewInt(old.ToInt() + val.ToInt())
				}
			case "-=":
				val = NewInt(old.ToInt() - val.ToInt())
			case "*=":
				val = NewInt(old.ToInt() * val.ToInt())
			case "/=":
				if val.ToInt() != 0 {
					val = NewInt(old.ToInt() / val.ToInt())
				} else {
					val = NewInt(0)
				}
			case "^=":
				val = NewInt(old.ToInt() ^ val.ToInt())
			}
		}
		arr.ArraySet(key, val)
		// 回写根变量
		if v, ok := t.Arr.(*VarExpr); ok {
			e.vars[v.Name] = arr
			e.globals[v.Name] = arr
		}
		// 回写对象属性：$obj->prop[$key] = val
		if pa, ok := t.Arr.(*PropertyAccess); ok {
			recv, err := e.evalExpr(pa.Receiver)
			if err == nil && recv.Kind == KindObject {
				recv.Object.Properties[pa.Prop] = arr
			}
		}
		return execResult{val: val}, nil
	}
	return execResult{}, nil
}

func (e *Env) execForeach(s *ForeachStmt) (execResult, error) {
	arr, err := e.evalExpr(s.Arr)
	if err != nil {
		return execResult{}, err
	}
	if arr.Kind != KindArray {
		return execResult{}, nil
	}
	for _, k := range arr.Keys {
		v := arr.Arr[k]
		if s.KeyVar != "" {
			e.vars[s.KeyVar] = NewString(k)
			e.globals[s.KeyVar] = NewString(k)
		}
		e.vars[s.ValVar] = v
		e.globals[s.ValVar] = v
		r, err := e.execBlock(s.Body)
		if err != nil {
			return r, err
		}
		if e.exitLoc {
			break
		}
		if r.flow == cfBreak {
			if r.count > 1 {
				return execResult{flow: cfBreak, count: r.count - 1}, nil
			}
			continue
		}
		if r.flow == cfContinue {
			if r.count > 1 {
				return execResult{flow: cfContinue, count: r.count - 1}, nil
			}
			continue
		}
		if r.flow == cfReturn {
			return r, nil
		}
	}
	return execResult{}, nil
}

func (e *Env) execFor(s *ForStmt) (execResult, error) {
	// init
	for _, st := range s.Init {
		if _, err := e.execStmt(st); err != nil {
			return execResult{}, err
		}
	}
	for {
		if s.Cond != nil {
			cv, err := e.evalExpr(s.Cond)
			if err != nil {
				return execResult{}, err
			}
			if !cv.ToBool() {
				break
			}
		}
		r, err := e.execBlock(s.Body)
		if err != nil {
			return r, err
		}
		if e.exitLoc {
			break
		}
		if r.flow == cfBreak {
			if r.count > 1 {
				return execResult{flow: cfBreak, count: r.count - 1}, nil
			}
			break
		}
		if r.flow == cfContinue {
			if r.count > 1 {
				return execResult{flow: cfContinue, count: r.count - 1}, nil
			}
		}
		if r.flow == cfReturn {
			return r, nil
		}
		// post
		for _, st := range s.Post {
			if _, err := e.execStmt(st); err != nil {
				return execResult{}, err
			}
		}
	}
	return execResult{}, nil
}

func (e *Env) execWhile(s *WhileStmt) (execResult, error) {
	for {
		cv, err := e.evalExpr(s.Cond)
		if err != nil {
			return execResult{}, err
		}
		if !cv.ToBool() {
			break
		}
		r, err := e.execBlock(s.Body)
		if err != nil {
			return r, err
		}
		if e.exitLoc {
			break
		}
		if r.flow == cfBreak {
			if r.count > 1 {
				return execResult{flow: cfBreak, count: r.count - 1}, nil
			}
			break
		}
		if r.flow == cfContinue {
			if r.count > 1 {
				return execResult{flow: cfContinue, count: r.count - 1}, nil
			}
			continue
		}
		if r.flow == cfReturn {
			return r, nil
		}
	}
	return execResult{}, nil
}

func (e *Env) execDoWhile(s *DoWhileStmt) (execResult, error) {
	for {
		r, err := e.execBlock(s.Body)
		if err != nil {
			return r, err
		}
		if e.exitLoc {
			break
		}
		if r.flow == cfBreak {
			if r.count > 1 {
				return execResult{flow: cfBreak, count: r.count - 1}, nil
			}
			break
		}
		if r.flow == cfContinue {
			if r.count > 1 {
				return execResult{flow: cfContinue, count: r.count - 1}, nil
			}
		}
		if r.flow == cfReturn {
			return r, nil
		}
		cv, err := e.evalExpr(s.Cond)
		if err != nil {
			return execResult{}, err
		}
		if !cv.ToBool() {
			break
		}
	}
	return execResult{}, nil
}

func (e *Env) execSwitch(s *SwitchStmt) (execResult, error) {
	subj, err := e.evalExpr(s.Subject)
	if err != nil {
		return execResult{}, err
	}
	matched := false
	for _, c := range s.Cases {
		if !matched {
			if c.IsDefault {
				matched = true
			} else {
				cv, err := e.evalExpr(c.Value)
				if err != nil {
					return execResult{}, err
				}
				if subj.ToString() == cv.ToString() {
					matched = true
				}
			}
		}
		if matched {
			r, err := e.execBlock(c.Body)
			if err != nil {
				return r, err
			}
			if e.exitLoc {
				break
			}
			if r.flow == cfBreak {
				if r.count > 1 {
					return execResult{flow: cfBreak, count: r.count - 1}, nil
				}
				return execResult{}, nil
			}
			if r.flow == cfContinue {
				if r.count > 1 {
					return execResult{flow: cfContinue, count: r.count - 1}, nil
				}
				return execResult{}, nil
			}
			if r.flow == cfReturn {
				return r, nil
			}
		}
	}
	return execResult{}, nil
}

// ---------------------------------------------------------------------------
// 表达式求值
// ---------------------------------------------------------------------------

func (e *Env) evalExpr(x Expr) (Value, error) {
	switch n := x.(type) {
	case *VarExpr:
		return e.evalVar(n.Name)
	case *ScalarInt:
		return NewInt(n.Val), nil
	case *ScalarFloat:
		return NewFloat(n.Val), nil
	case *ScalarStr:
		return NewString(n.Val), nil
	case *InterpolatedStr:
		var b strings.Builder
		for _, part := range n.Parts {
			switch p := part.(type) {
			case string:
				b.WriteString(p)
			case Expr:
				v, err := e.evalExpr(p)
				if err != nil {
					return v, err
				}
				b.WriteString(v.ToString())
			}
		}
		return NewString(b.String()), nil
	case *ConstBool:
		return NewBool(n.Val), nil
	case *ConstNull:
		return NewNull(), nil
	case *ArrayExpr:
		v := NewArray()
		if n.Keys == nil {
			for _, el := range n.Values {
				ev, err := e.evalExpr(el)
				if err != nil {
					return ev, err
				}
				v.ArraySet(NewInt(int64(len(v.Keys))), ev)
			}
		} else {
			for i, k := range n.Keys {
				kv, err := e.evalExpr(k)
				if err != nil {
					return kv, err
				}
				vv, err := e.evalExpr(n.Values[i])
				if err != nil {
					return vv, err
				}
				v.ArraySet(kv, vv)
			}
		}
		return v, nil
	case *BinaryExpr:
		return e.evalBinary(n)
	case *UnaryExpr:
		v, err := e.evalExpr(n.Expr)
		if err != nil {
			return v, err
		}
		switch n.Op {
		case "!":
			return NewBool(!v.ToBool()), nil
		case "-":
			return NewInt(-v.ToInt()), nil
		}
		return NewNull(), fmt.Errorf("runtime: 未知一元运算符 %s", n.Op)
	case *IndexExpr:
		arr, err := e.evalExpr(n.Arr)
		if err != nil {
			return arr, err
		}
		key, err := e.evalExpr(n.Key)
		if err != nil {
			return key, err
		}
		return arr.ArrayGet(key), nil
	case *FuncCall:
		return e.callFunc(n.Name, n.Args)
	case *MethodCall:
		recv, err := e.evalExpr(n.Receiver)
		if err != nil {
			return recv, err
		}
		// 对象方法调用
		if recv.Kind == KindObject {
			cls := e.classes[recv.Object.ClassName]
			if cls != nil {
				for _, m := range cls.Methods {
					if m.Name == n.Method {
						// 保存/恢复 $this 和 __current_class__
						oldThis, hadThis := e.vars["this"]
						oldClass, hadClass := e.vars["__current_class__"]
						e.vars["this"] = recv
						e.vars["__current_class__"] = NewString(recv.Object.ClassName)
						result, err := e.callMethod(m, n.Args)
						if hadThis {
							e.vars["this"] = oldThis
						} else {
							delete(e.vars, "this")
						}
						if hadClass {
							e.vars["__current_class__"] = oldClass
						} else {
							delete(e.vars, "__current_class__")
						}
						return result, err
					}
				}
			}
			return NewNull(), nil
		}
		// 支持异常对象方法调用：$e->getMessage(), $e->getCode() 等
		if recv.Kind == KindArray {
			switch n.Method {
			case "getMessage":
				return recv.ArrayGet(NewString("message")), nil
			case "getCode":
				return NewInt(0), nil
			case "getLine", "getFile", "getTraceAsString", "getPrevious":
				return NewString(""), nil
			}
		}
		return NewNull(), nil
	case *StaticCall:
		// 特殊处理：DateTime::createFromFormat
		className := n.Class
		method := n.Method
		if strings.EqualFold(className, "DateTime") && strings.EqualFold(method, "createFromFormat") {
			// DateTime::createFromFormat($format, $value)
			// 简化实现：如果 $value 能匹配 $format 的基本模式，返回非 null
			if len(n.Args) >= 2 {
				formatVal, _ := e.evalExpr(n.Args[0])
				dateVal, _ := e.evalExpr(n.Args[1])
				formatStr := formatVal.ToString()
				dateStr := dateVal.ToString()
				// 简单验证：Y-m-d 格式对应 YYYY-MM-DD
				if formatStr == "Y-m-d" {
					// 检查 YYYY-MM-DD 格式
					if len(dateStr) == 10 && dateStr[4] == '-' && dateStr[7] == '-' {
						return NewString(dateStr), nil
					}
				}
				// 其它格式：非空就认为有效
				if dateStr != "" {
					return NewString(dateStr), nil
				}
			}
			return NewNull(), nil
		}
		// self::method() 或 static::method() — 调用当前类的方法
		if className == "self" || className == "static" {
			if curClass, ok := e.vars["__current_class__"]; ok && curClass.Kind == KindString {
				className = curClass.Str
			}
		}
		// 查找类方法
		if cls, ok := e.classes[className]; ok {
			for _, m := range cls.Methods {
				if m.Name == method {
					// 保存当前 $this 和 __current_class__
					savedThis := e.vars["this"]
					savedClass := e.vars["__current_class__"]
					// 静态方法：$this 为 null
					e.vars["this"] = NewNull()
					e.vars["__current_class__"] = NewString(className)
					result, err := e.callMethod(m, n.Args)
					e.vars["this"] = savedThis
					e.vars["__current_class__"] = savedClass
					return result, err
				}
			}
		}
		// 其它静态调用返回 null
		return NewNull(), nil
	case *PropertyAccess:
		recv, err := e.evalExpr(n.Receiver)
		if err != nil {
			return recv, err
		}
		// 对象属性访问
		if recv.Kind == KindObject {
			if v, ok := recv.Object.Properties[n.Prop]; ok {
				return v, nil
			}
			return NewNull(), nil
		}
		// PHP $obj->prop 在无对象系统时，当作关联数组的 key 访问
		return recv.ArrayGet(NewString(n.Prop)), nil
	case *TernaryExpr:
		c, err := e.evalExpr(n.Cond)
		if err != nil {
			return c, err
		}
		if c.ToBool() {
			if n.Then == nil {
				return c, nil
			}
			return e.evalExpr(n.Then)
		}
		return e.evalExpr(n.Else)
	case *NullCoalesceExpr:
		l, err := e.evalExpr(n.Left)
		if err != nil {
			return l, err
		}
		if !l.IsNull() {
			return l, nil
		}
		return e.evalExpr(n.Right)
	case *AssignExpr:
		return e.evalAssignExpr(n)
	case *InstanceOfExpr:
		// instanceof 简化实现：检测左侧值是否为指定类的实例
		v, err := e.evalExpr(n.Expr)
		if err != nil {
			return NewBool(false), nil
		}
		// null/null 值永远不是任何类的实例
		if v.Kind == KindNull {
			return NewBool(false), nil
		}
		// DateTime 特例：phpgo 没有真正的 DateTime 类，
		// 但 DateTime::createFromFormat 返回的值非 null 时认为 true
		className := strings.ToLower(n.Class)
		if className == "datetime" {
			return NewBool(v.Kind != KindNull), nil
		}
		// 通用：非 null 值视为 true（简化）
		return NewBool(true), nil
	case *CastExpr:
		v, err := e.evalExpr(n.Expr)
		if err != nil {
			return v, err
		}
		switch n.Kind {
		case "int":
			return NewInt(v.ToInt()), nil
		case "float":
			return NewFloat(float64(v.ToInt())), nil
		case "bool":
			return NewBool(v.ToBool()), nil
		case "string":
			return NewString(v.ToString()), nil
		case "array":
			if v.Kind == KindArray {
				return v, nil
			}
			arr := NewArray()
			arr.ArraySet(NewInt(0), v)
			return arr, nil
		case "object":
			// (object) 将数组转为 stdClass 对象，键变成属性
			if v.Kind == KindArray {
				obj := NewObject("stdClass")
				for _, k := range v.Keys {
					obj.Object.Properties[k] = v.ArrayGet(NewString(k))
				}
				return obj, nil
			}
			// 非数组：包装为空对象
			return NewObject("stdClass"), nil
		}
		return v, nil
	case *ClosureExpr:
		// 闭包：捕获当前作用域的 use 变量
		captured := map[string]Value{}
		for i, name := range n.Uses {
			if i < len(n.ByRef) && n.ByRef[i] {
				// 引用捕获：后续修改影响外层（简化为值共享）
				captured[name] = e.vars[name]
			} else {
				captured[name] = e.vars[name]
			}
		}
		fn := &FuncDecl{
			Name:   "__closure_" + fmt.Sprintf("%p", n),
			Params: n.Params,
			Body:   n.Body,
		}
		e.funcs[fn.Name] = fn
		// 存 captured 到 env 以便后续调用时恢复
		arr := NewArray()
		arr.ArraySet(NewString("__closure_name"), NewString(fn.Name))
		arr.ArraySet(NewString("__captured"), NewMapValue(captured))
		return arr, nil
	case *EmptyExpr:
		v, err := e.evalExpr(n.E)
		if err != nil {
			return v, err
		}
		return NewBool(!v.ToBool()), nil
	case *IssetExpr:
		for _, arg := range n.Args {
			v, err := e.evalExpr(arg)
			if err != nil {
				return NewBool(false), nil
			}
			if v.IsNull() {
				return NewBool(false), nil
			}
		}
		return NewBool(true), nil
	case *MagicConstExpr:
		switch n.Name {
		case "__DIR__":
			if e.scriptPath != "" {
				return NewString(filepath.Dir(e.scriptPath)), nil
			}
			return NewString("."), nil
		case "__FILE__":
			return NewString(e.scriptPath), nil
		case "__LINE__":
			return NewInt(0), nil
		}
		return NewString(""), nil
	case *ConstExpr:
		// 运行时常量：查 e.consts（define 注册的）
		if v, ok := e.consts[n.Name]; ok {
			return v, nil
		}
		// 找不到则当字符串名（PHP 行为）
		return NewString(n.Name), nil
	case *NewExpr:
		// 检查是否是已注册的用户类
		if cls, ok := e.classes[n.Class]; ok {
			obj := NewObject(n.Class)
			// 初始化属性
			for _, prop := range cls.Properties {
				if prop.Default != nil {
					pv, err := e.evalExpr(prop.Default)
					if err != nil {
						return pv, err
					}
					obj.Object.Properties[prop.Name] = pv
				} else {
					obj.Object.Properties[prop.Name] = NewNull()
				}
			}
			// 调用构造函数
			for _, m := range cls.Methods {
				if m.Name == "__construct" {
					// 保存当前 $this，设置新的
					oldThis, hadThis := e.vars["this"]
					e.vars["this"] = obj
					_, err := e.callMethod(m, n.Args)
					if hadThis {
						e.vars["this"] = oldThis
					} else {
						delete(e.vars, "this")
					}
					if err != nil {
						return obj, err
					}
					break
				}
			}
			return obj, nil
		}
		// new Exception/RuntimeException/Throwable
		// 取第一个参数作为 message
		var msg string
		if len(n.Args) > 0 {
			v, err := e.evalExpr(n.Args[0])
			if err != nil {
				return v, err
			}
			msg = v.ToString()
		}
		// 返回一个关联数组模拟异常对象
		excVar := NewArray()
		excVar.ArraySet(NewString("message"), NewString(msg))
		excVar.ArraySet(NewString("class"), NewString(n.Class))
		return excVar, nil
	case *ThisExpr:
		if v, ok := e.vars["this"]; ok {
			return v, nil
		}
		return NewNull(), nil
	case *SelfConstExpr:
		// self::CONSTANT 或 ClassName::CONSTANT
		className := n.Class
		if className == "self" {
			if curClass, ok := e.vars["__current_class__"]; ok && curClass.Kind == KindString {
				className = curClass.Str
			}
		}
		// 查找类常量
		if cls, ok := e.classes[className]; ok {
			for _, c := range cls.Consts {
				if c.Name == n.Name {
					return e.evalExpr(c.Val)
				}
			}
		}
		// 查找全局常量 ClassName::CONSTNAME
		if v, ok := e.consts[className+"::"+n.Name]; ok {
			return v, nil
		}
		return NewNull(), nil
	case *SplatExpr:
		// ...$var 在非调用上下文中返回数组本身
		return e.evalExpr(n.Expr)
	case *varRef:
		// &$var 作实参：返回当前值（内置函数若在 refParams 中则由调用侧构造引用写回）
		return e.vars[n.name], nil
	}
	return NewNull(), fmt.Errorf("runtime: 未知表达式节点 %T", x)
}

func (e *Env) evalVar(name string) (Value, error) {
	switch name {
	case "_GET":
		v := NewArray()
		for k, val := range e.get {
			v.ArraySet(NewString(k), NewString(val))
		}
		return v, nil
	case "_SERVER":
		v := NewArray()
		for k, val := range e.server {
			v.ArraySet(NewString(k), NewString(val))
		}
		return v, nil
	case "_POST":
		v := NewArray()
		for k, val := range e.post {
			v.ArraySet(NewString(k), NewString(val))
		}
		return v, nil
	case "_COOKIE":
		v := NewArray()
		for k, val := range e.cookie {
			v.ArraySet(NewString(k), NewString(val))
		}
		return v, nil
	case "_REQUEST":
		v := NewArray()
		for k, val := range e.get {
			v.ArraySet(NewString(k), NewString(val))
		}
		for k, val := range e.post {
			v.ArraySet(NewString(k), NewString(val))
		}
		return v, nil
	case "_SESSION":
		v := NewArray()
		for k, val := range e.session {
			v.ArraySet(NewString(k), NewString(val))
		}
		return v, nil
	case "_ENV":
		v := NewArray()
		for k, val := range e.envmap {
			v.ArraySet(NewString(k), NewString(val))
		}
		return v, nil
	case "_FILES":
		v := NewArray()
		for k, val := range e.ufiles {
			v.ArraySet(NewString(k), val)
		}
		return v, nil
	case "GLOBALS":
		v := NewArray()
		for k, val := range e.globals {
			v.ArraySet(NewString(k), val)
		}
		return v, nil
	}
	if v, ok := e.vars[name]; ok {
		return v, nil
	}
	return NewNull(), nil
}

func (e *Env) evalBinary(n *BinaryExpr) (Value, error) {
	// 短路逻辑运算
	switch n.Op {
	case "and", "&&":
		l, err := e.evalExpr(n.Left)
		if err != nil {
			return l, err
		}
		if !l.ToBool() {
			return NewBool(false), nil
		}
		r, err := e.evalExpr(n.Right)
		if err != nil {
			return r, err
		}
		return NewBool(r.ToBool()), nil
	case "or", "||":
		l, err := e.evalExpr(n.Left)
		if err != nil {
			return l, err
		}
		if l.ToBool() {
			return NewBool(true), nil
		}
		r, err := e.evalExpr(n.Right)
		if err != nil {
			return r, err
		}
		return NewBool(r.ToBool()), nil
	}

	l, err := e.evalExpr(n.Left)
	if err != nil {
		return l, err
	}
	r, err := e.evalExpr(n.Right)
	if err != nil {
		return r, err
	}
	switch n.Op {
	case ".", ".=":
		return Value{Kind: KindString, Str: l.ToString() + r.ToString()}, nil
	case "+", "+=":
		if l.Kind == KindString || r.Kind == KindString {
			return Value{Kind: KindString, Str: l.ToString() + r.ToString()}, nil
		}
		// 数组 + 数组 = 合并
		if l.Kind == KindArray && r.Kind == KindArray {
			result := NewArray()
			for _, k := range l.Keys {
				result.ArraySet(NewString(k), l.Arr[k])
			}
			for _, k := range r.Keys {
				if _, ok := result.Arr[k]; !ok {
					result.ArraySet(NewString(k), r.Arr[k])
				}
			}
			return result, nil
		}
		// PHP 语义：任一侧为浮点 → 浮点结果
		if l.Kind == KindFloat || r.Kind == KindFloat {
			return NewFloat(l.ToFloat() + r.ToFloat()), nil
		}
		return NewInt(l.ToInt() + r.ToInt()), nil
	case "-", "-=":
		if l.Kind == KindFloat || r.Kind == KindFloat {
			return NewFloat(l.ToFloat() - r.ToFloat()), nil
		}
		return NewInt(l.ToInt() - r.ToInt()), nil
	case "*", "*=":
		if l.Kind == KindFloat || r.Kind == KindFloat {
			return NewFloat(l.ToFloat() * r.ToFloat()), nil
		}
		return NewInt(l.ToInt() * r.ToInt()), nil
	case "/", "/=":
		if r.ToFloat() == 0 {
			return NewInt(0), nil
		}
		// PHP 除法恒为浮点（除非整数能整除也返回 float）
		return NewFloat(l.ToFloat() / r.ToFloat()), nil
	case "%":
		if r.ToInt() == 0 {
			return NewInt(0), nil
		}
		return NewInt(l.ToInt() % r.ToInt()), nil
	case "==":
		return NewBool(l.ToString() == r.ToString()), nil
	case "===":
		if l.Kind != r.Kind {
			return NewBool(false), nil
		}
		return NewBool(l.ToString() == r.ToString()), nil
	case "!=":
		return NewBool(l.ToString() != r.ToString()), nil
	case "!==":
		if l.Kind != r.Kind {
			return NewBool(true), nil
		}
		return NewBool(l.ToString() != r.ToString()), nil
	case "<":
		if l.Kind == KindString || r.Kind == KindString {
			return NewBool(l.ToString() < r.ToString()), nil
		}
		return NewBool(l.ToInt() < r.ToInt()), nil
	case ">":
		if l.Kind == KindString || r.Kind == KindString {
			return NewBool(l.ToString() > r.ToString()), nil
		}
		return NewBool(l.ToInt() > r.ToInt()), nil
	case "<=":
		if l.Kind == KindString || r.Kind == KindString {
			return NewBool(l.ToString() <= r.ToString()), nil
		}
		return NewBool(l.ToInt() <= r.ToInt()), nil
	case ">=":
		if l.Kind == KindString || r.Kind == KindString {
			return NewBool(l.ToString() >= r.ToString()), nil
		}
		return NewBool(l.ToInt() >= r.ToInt()), nil
	case "^", "^=":
		return NewInt(l.ToInt() ^ r.ToInt()), nil
	case "xor":
		return NewBool(l.ToBool() != r.ToBool()), nil
	case "&":
		return NewInt(l.ToInt() & r.ToInt()), nil
	case "|":
		return NewInt(l.ToInt() | r.ToInt()), nil
	case "<<":
		return NewInt(l.ToInt() << r.ToInt()), nil
	case ">>":
		return NewInt(l.ToInt() >> r.ToInt()), nil
	}
	return NewNull(), fmt.Errorf("runtime: 未知运算符 %s", n.Op)
}

// evalAssignExpr 处理赋值表达式
func (e *Env) evalAssignExpr(n *AssignExpr) (Value, error) {
	val, err := e.evalExpr(n.Val)
	if err != nil {
		return val, err
	}
	switch t := n.Target.(type) {
	case *VarExpr:
		e.vars[t.Name] = val
		e.globals[t.Name] = val
		return val, nil
	case *IndexExpr:
		// $arr[$key] = val
		if v, ok := t.Arr.(*VarExpr); ok {
			arr := e.vars[v.Name]
			if arr.Kind != KindArray {
				arr = NewArray()
			}
			key, err := e.evalExpr(t.Key)
			if err != nil {
				return val, err
			}
			arr.ArraySet(key, val)
			e.vars[v.Name] = arr
			e.globals[v.Name] = arr
			return val, nil
		}
		// $obj->prop[$key] = val（对象属性的数组下标赋值）
		if pa, ok := t.Arr.(*PropertyAccess); ok {
			recv, err := e.evalExpr(pa.Receiver)
			if err != nil {
				return val, err
			}
			key, err := e.evalExpr(t.Key)
			if err != nil {
				return val, err
			}
			if recv.Kind == KindObject {
				// 获取属性数组（或创建）
				arr, ok := recv.Object.Properties[pa.Prop]
				if !ok || arr.Kind != KindArray {
					arr = NewArray()
				}
				arr.ArraySet(key, val)
				recv.Object.Properties[pa.Prop] = arr
				return val, nil
			}
		}
	case *PropertyAccess:
		// $this->prop = val 或 $obj->prop = val
		recv, err := e.evalExpr(t.Receiver)
		if err != nil {
			return val, err
		}
		if recv.Kind == KindObject {
			recv.Object.Properties[t.Prop] = val
			return val, nil
		}
		// 非对象（数组模拟）回退
		recv.ArraySet(NewString(t.Prop), val)
		return val, nil
	case *FuncCall:
		// __list 赋值：list($a, $b) = $arr
		if t.Name == "__list" {
			for i, arg := range t.Args {
				if _, ok := arg.(*ConstNull); ok {
					continue
				}
				elem := val.ArrayGet(NewInt(int64(i)))
				if v, ok := arg.(*VarExpr); ok {
					e.vars[v.Name] = elem
					e.globals[v.Name] = elem
				}
				if inner, ok := arg.(*ArrayExpr); ok {
					e.destructureArray(inner, elem)
				}
			}
			return val, nil
		}
	case *ArrayExpr:
		// [$a, $b] = $arr（PHP 短数组解构）
		e.destructureArray(t, val)
		return val, nil
	}
	return val, nil
}

// destructureArray 按 PHP list/短数组解构语义把数组值拆给目标变量（支持嵌套与空位跳过）
func (e *Env) destructureArray(targets *ArrayExpr, arr Value) {
	for i, el := range targets.Values {
		if _, ok := el.(*ConstNull); ok {
			continue
		}
		elem := arr.ArrayGet(NewInt(int64(i)))
		if v, ok := el.(*VarExpr); ok {
			e.vars[v.Name] = elem
			e.globals[v.Name] = elem
		}
		if inner, ok := el.(*ArrayExpr); ok {
			e.destructureArray(inner, elem)
		}
	}
}

// callFunc 处理函数调用（内置 + 用户函数）
func (e *Env) callFunc(name string, args []Expr) (Value, error) {
	// 先试内置
	if bf, ok := builtins[name]; ok {
		var vs []Value
		// 需要引用参数的内置函数：第 N 个参数（0-based）需要按引用传递
		refParams := map[string]map[int]bool{
			"preg_match":                  {2: true},
			"preg_match_all":              {2: true},
			"preg_replace_callback":       {3: true},
			"preg_replace_callback_array": {3: true},
			"parse_str":                   {1: true},
			"curl_multi_exec":             {1: true},
			// 原地修改数组/变量的函数（第 0 参按引用传递，供 writeRef 写回）
			"sort":         {0: true},
			"rsort":        {0: true},
			"asort":        {0: true},
			"ksort":        {0: true},
			"arsort":       {0: true},
			"krsort":       {0: true},
			"usort":        {0: true},
			"uasort":       {0: true},
			"uksort":       {0: true},
			"shuffle":      {0: true},
			"array_splice": {0: true},
			"settype":      {0: true},
		}
		for i, a := range args {
			// 特定函数的特定参数按引用传递（供 writeRef 写回）
			if refIdxs, ok := refParams[name]; ok && refIdxs[i] {
				if v, ok := a.(*VarExpr); ok {
					cur := e.vars[v.Name]
					rv := NewRef(&varRef{name: v.Name})
					rv.RefVal = &cur
					vs = append(vs, rv)
					continue
				}
			}
			// splat 展开：...$var
			if sp, ok := a.(*SplatExpr); ok {
				val, err := e.evalExpr(sp.Expr)
				if err != nil {
					return val, err
				}
				if val.Kind == KindArray {
					for _, k := range val.Keys {
						vs = append(vs, val.Arr[k])
					}
				}
				continue
			}
			val, err := e.evalExpr(a)
			if err != nil {
				return val, err
			}
			vs = append(vs, val)
		}
		return bf(e, vs)
	}
	// 用户函数
	fn, ok := e.funcs[name]
	if !ok {
		// PHP 中调用未定义函数会 warning 并返回 null
		return NewNull(), nil
	}
	// 新作用域
	saved := map[string]Value{}
	for k, v := range e.vars {
		saved[k] = v
	}
	// 绑定参数
	for i, p := range fn.Params {
		if i < len(args) {
			v, err := e.evalExpr(args[i])
			if err != nil {
				return v, err
			}
			e.vars[p.Name] = v.Clone()
		} else if p.Default != nil {
			v, err := e.evalExpr(p.Default)
			if err != nil {
				return v, err
			}
			e.vars[p.Name] = v
		} else {
			e.vars[p.Name] = NewNull()
		}
	}
	r, err := e.execBlock(fn.Body)
	defer func() {
		e.vars = saved
	}()
	if err != nil {
		return NewNull(), err
	}
	if r.flow == cfReturn {
		return r.val, nil
	}
	return NewNull(), nil
}

// callMethod 调用类方法（$this 已在 e.vars 中设置）
func (e *Env) callMethod(fn *FuncDecl, args []Expr) (Value, error) {
	// 保存当前作用域（但保留 $this 和 __current_class__）
	saved := map[string]Value{}
	for k, v := range e.vars {
		if k == "this" || k == "__current_class__" {
			continue
		}
		saved[k] = v
	}
	// 保留 this 和 curClass
	thisVal := e.vars["this"]
	curClass := e.vars["__current_class__"]

	// 预求值参数（在旧作用域中求值，含 splat 展开）
	var flatVals []Value
	for _, a := range args {
		if sp, ok := a.(*SplatExpr); ok {
			val, err := e.evalExpr(sp.Expr)
			if err != nil {
				return val, err
			}
			if val.Kind == KindArray {
				for _, k := range val.Keys {
					flatVals = append(flatVals, val.Arr[k])
				}
			}
			continue
		}
		val, err := e.evalExpr(a)
		if err != nil {
			return val, err
		}
		flatVals = append(flatVals, val)
	}

	// 清空非 this 变量，绑定参数
	e.vars = map[string]Value{}
	e.vars["this"] = thisVal
	e.vars["__current_class__"] = curClass

	// 绑定参数
	for i, p := range fn.Params {
		if i < len(flatVals) {
			e.vars[p.Name] = flatVals[i].Clone()
		} else if p.Default != nil {
			v, err := e.evalExpr(p.Default)
			if err != nil {
				e.vars = saved
				e.vars["this"] = thisVal
				e.vars["__current_class__"] = curClass
				return v, err
			}
			e.vars[p.Name] = v
		} else {
			e.vars[p.Name] = NewNull()
		}
	}
	r, err := e.execBlock(fn.Body)
	// 恢复作用域
	e.vars = saved
	e.vars["this"] = thisVal
	e.vars["__current_class__"] = curClass
	if err != nil {
		return NewNull(), err
	}
	if r.flow == cfReturn {
		return r.val, nil
	}
	return NewNull(), nil
}
func (e *Env) callUserFuncValues(fn *FuncDecl, vs []Value) (Value, error) {
	saved := map[string]Value{}
	for k, v := range e.vars {
		saved[k] = v
	}
	for i, p := range fn.Params {
		if i < len(vs) {
			e.vars[p.Name] = vs[i]
		} else if p.Default != nil {
			v, err := e.evalExpr(p.Default)
			if err != nil {
				return v, err
			}
			e.vars[p.Name] = v
		} else {
			e.vars[p.Name] = NewNull()
		}
	}
	r, err := e.execBlock(fn.Body)
	defer func() {
		e.vars = saved
	}()
	if err != nil {
		return NewNull(), err
	}
	if r.flow == cfReturn {
		return r.val, nil
	}
	return NewNull(), nil
}

// writeOutput 写入输出缓冲（支持 ob 控制）
func (e *Env) writeOutput(s string) {
	if len(e.obStack) > 0 {
		e.obStack[len(e.obStack)-1].WriteString(s)
	} else {
		e.echoOut.WriteString(s)
	}
}

// NewMapValue 从 map 创建数组 Value
func NewMapValue(m map[string]Value) Value {
	v := NewArray()
	// 排序 keys 保证确定性
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v.ArraySet(NewString(k), m[k])
	}
	return v
}

// PHPException 表示 PHP 异常（try-catch 中抛出和捕获）
type PHPException struct {
	Msg   string
	Class string // 异常类名（RuntimeException, Exception, Throwable 等）
}

func (e *PHPException) Error() string { return e.Msg }

// execTry 执行 try-catch-finally
func (e *Env) execTry(s *TryStmt) (execResult, error) {
	// 执行 try body
	r, err := e.execBlock(s.Body)
	if err != nil {
		// 检查是否是 PHPException
		if exc, ok := err.(*PHPException); ok {
			// 尝试匹配 catch
			for _, c := range s.Catches {
				if matchException(exc, c.Types) {
					// 设置异常变量
					excVar := NewArray()
					excVar.ArraySet(NewString("message"), NewString(exc.Msg))
					excVar.ArraySet(NewString("class"), NewString(exc.Class))
					if c.Var != "" {
						e.vars[c.Var] = excVar
						e.globals[c.Var] = excVar
					}
					// 执行 catch body
					cr, cerr := e.execBlock(c.Body)
					if cerr != nil {
						// catch body 中又 throw 了
						return cr, cerr
					}
					// 执行 finally
					if s.Finally != nil {
						fr, ferr := e.execBlock(s.Finally)
						if ferr != nil {
							return fr, ferr
						}
						if fr.flow != cfNormal {
							return fr, nil
						}
					}
					return cr, nil
				}
			}
			// 没有匹配的 catch，执行 finally 后重新抛出
			if s.Finally != nil {
				e.execBlock(s.Finally)
			}
			return execResult{}, err
		}
		// 非异常错误，直接返回
		return r, err
	}
	// try body 正常完成，执行 finally
	if s.Finally != nil {
		fr, ferr := e.execBlock(s.Finally)
		if ferr != nil {
			return fr, ferr
		}
		if fr.flow != cfNormal {
			return fr, nil
		}
	}
	return r, nil
}

// matchException 检查异常是否匹配 catch 的类型列表
func matchException(exc *PHPException, types []string) bool {
	if len(types) == 0 {
		return true // 无类型限制，匹配所有
	}
	for _, t := range types {
		// Throwable 匹配所有异常
		if t == "Throwable" || t == "Exception" || t == "\\Throwable" || t == "\\Exception" {
			return true
		}
		// 精确匹配类名
		if t == exc.Class || t == "\\"+exc.Class {
			return true
		}
	}
	return false
}
