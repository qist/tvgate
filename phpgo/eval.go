package phpgo

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

// ProxyFunc 是 Go 实现的 HTTP 请求能力（替代 curl + 代理）。
type ProxyFunc func(method, url string, opts *CurlOptions) (*ProxyResult, error)

// ProxyResult 是 proxy 请求的结果
type ProxyResult struct {
	Body         string
	StatusCode   int
	Location    string // 重定向 URL（如果有）
	ContentType string
	EffectiveURL string // 最终 URL（跟随重定向后）
}

// CurlOptions 对应 PHP curl_setopt 的关键选项
type CurlOptions struct {
	Proxy       string // CURLOPT_PROXY
	ProxyType   string // CURLOPT_PROXYTYPE
	Headers     []string
	PostData    string
	Timeout     int
	UserAgent   string
	Method      string
	FollowRedirect bool
	SkipSSL     bool   // CURLOPT_SSL_VERIFYPEER=false
}

// Env 执行环境
type Env struct {
	vars      map[string]Value
	funcs     map[string]*FuncDecl
	globals   map[string]Value // $GLOBALS
	consts    map[string]Value
	server    map[string]string // $_SERVER
	get       map[string]string // $_GET
	post      map[string]string // $_POST
	cookie    map[string]string // $_COOKIE
	session   map[string]string // $_SESSION
	envmap    map[string]string // $_ENV
	ufiles    map[string]Value  // $_FILES
	reqURI    string            // $_SERVER["REQUEST_URI"]
	phpInput  string            // php://input 原始请求体
	headers        []string          // 捕获的 header() 输出
	statusCode     int               // 显式设置的状态码（如 header("HTTP/1.1 404")）
	statusCodeSet  bool              // 是否显式设置过状态码
	exitLoc        bool              // 触发 exit
	exitVal        Value             // exit() 参数值
	proxy     ProxyFunc
	echoOut   *strings.Builder
	obStack   []*strings.Builder // output buffer stack
	breakN    int                // 待处理的 break 层数
	continueN int                // 待处理的 continue 层数
	files    map[int]io.ReadWriteCloser // 文件/流句柄表（fd -> 资源）
	nextFd   int                        // 下一个可用 fd
	scriptPath string                     // 当前脚本路径（用于 __DIR__/__FILE__）
}

// NewEnv 创建执行环境
func NewEnv(proxy ProxyFunc) *Env {
	ev := &Env{
		vars:     map[string]Value{},
		funcs:    map[string]*FuncDecl{},
		globals:  map[string]Value{},
		consts:   defaultPHPConsts(),
		server:   map[string]string{},
		get:      map[string]string{},
		post:     map[string]string{},
		cookie:   map[string]string{},
		session:  map[string]string{},
		envmap:   map[string]string{},
		ufiles:   map[string]Value{},
		proxy:    proxy,
		echoOut:  &strings.Builder{},
		files:    map[int]io.ReadWriteCloser{},
		nextFd:   1,
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
	// CURLINFO
	c["CURLINFO_HTTP_CODE"] = NewInt(2097154)
	c["CURLINFO_RESPONSE_CODE"] = NewInt(2097154)
	c["CURLINFO_EFFECTIVE_URL"] = NewInt(1048577)
	c["CURLINFO_CONTENT_TYPE"] = NewInt(1048593)
	c["CURLINFO_TOTAL_TIME"] = NewInt(3145731)
	c["CURLINFO_URL"] = NewInt(1048577)
	c["CURLINFO_REDIRECT_URL"] = NewInt(3145744)
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
	// 逐层下钻
	cur := &root
	for i, idxExpr := range s.Indices {
		if idxExpr == nil {
			// 追加
			key := NewInt(int64(len(cur.Keys)))
			if i == len(s.Indices)-1 {
				val, err := e.evalExpr(s.Val)
				if err != nil {
					return execResult{}, err
				}
				cur.ArraySet(key, val)
			} else {
				child := NewArray()
				cur.ArraySet(key, child)
				cur = &child
			}
		} else {
			key, err := e.evalExpr(idxExpr)
			if err != nil {
				return execResult{}, err
			}
			if i == len(s.Indices)-1 {
				val, err := e.evalExpr(s.Val)
				if err != nil {
					return execResult{}, err
				}
				cur.ArraySet(key, val)
			} else {
				child := cur.ArrayGet(key)
				if child.Kind != KindArray {
					child = NewArray()
				}
				cur.ArraySet(key, child)
				cur = &child
			}
		}
	}
	// 回写根变量
	if v, ok := s.Base.(*VarExpr); ok {
		e.vars[v.Name] = root
		e.globals[v.Name] = root
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
		// 支持异常对象方法调用：$e->getMessage(), $e->getCode() 等
		recv, err := e.evalExpr(n.Receiver)
		if err != nil {
			return recv, err
		}
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
		// Class::method() 暂不实现对象系统
		// 特殊处理：DateTime::createFromFormat 等返回 null
		return NewNull(), nil
	case *PropertyAccess:
		// PHP $obj->prop 在无对象系统时，当作关联数组的 key 访问
		recv, err := e.evalExpr(n.Receiver)
		if err != nil {
			return recv, err
		}
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
	case "&&":
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
	case "||":
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
	case ".":
		return Value{Kind: KindString, Str: l.ToString() + r.ToString()}, nil
	case "+":
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
		return NewInt(l.ToInt() + r.ToInt()), nil
	case "-":
		return NewInt(l.ToInt() - r.ToInt()), nil
	case "*":
		return NewInt(l.ToInt() * r.ToInt()), nil
	case "/":
		if r.ToInt() == 0 {
			return NewInt(0), nil
		}
		return NewInt(l.ToInt() / r.ToInt()), nil
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
			}
			return val, nil
		}
	}
	return val, nil
}

// callFunc 处理函数调用（内置 + 用户函数）
func (e *Env) callFunc(name string, args []Expr) (Value, error) {
	// 先试内置
	if bf, ok := builtins[name]; ok {
		var vs []Value
		// 需要引用参数的内置函数：第 N 个参数（0-based）需要按引用传递
		refParams := map[string]map[int]bool{
			"preg_match":         {2: true},
			"preg_match_all":     {2: true},
			"preg_replace_callback": {3: true},
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
			e.vars[p.Name] = v
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

// callUserFuncValues 用已求值的 Value 列表调用用户函数（供 array_map/array_filter 等使用）
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
