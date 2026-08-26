package phpgo

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// ProxyFunc 是 Go 实现的 HTTP 请求能力（替代 curl + 代理）。
type ProxyFunc func(method, url string, opts *CurlOptions) (body string, err error)

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
	headers   []string          // 捕获的 header() 输出
	exitLoc   bool              // 触发 exit
	exitVal   Value             // exit() 参数值
	proxy     ProxyFunc
	echoOut   *strings.Builder
	obStack   []*strings.Builder // output buffer stack
	breakN    int                // 待处理的 break 层数
	continueN int                // 待处理的 continue 层数
	files    map[int]io.ReadWriteCloser // 文件/流句柄表（fd -> 资源）
	nextFd   int                        // 下一个可用 fd
}

// NewEnv 创建执行环境
func NewEnv(proxy ProxyFunc) *Env {
	return &Env{
		vars:     map[string]Value{},
		funcs:    map[string]*FuncDecl{},
		globals:  map[string]Value{},
		consts:   map[string]Value{},
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
				base, err := e.evalExpr(a.Arr)
				if err != nil || base.Kind != KindArray {
					continue
				}
				kv, err := e.evalExpr(a.Key)
				if err != nil {
					continue
				}
				base.ArrayUnset(kv)
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
		// PoC：不实现对象方法调用
		return NewNull(), fmt.Errorf("runtime: 暂不支持对象方法调用 ->%s", n.Method)
	case *StaticCall:
		// Class::method() 暂不实现对象系统
		// 特殊处理：DateTime::createFromFormat 等返回 null
		return NewNull(), nil
	case *PropertyAccess:
		return NewNull(), nil
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
		return NewBool(l.ToString() < r.ToString()), nil
	case ">":
		return NewBool(l.ToString() > r.ToString()), nil
	case "<=":
		return NewBool(l.ToString() <= r.ToString()), nil
	case ">=":
		return NewBool(l.ToString() >= r.ToString()), nil
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
		for _, a := range args {
			// 引用参数（&$var）：不求值，直接包成引用值
			if ar, ok := a.(assignable); ok {
				vs = append(vs, NewRef(ar))
				continue
			}
			v, err := e.evalExpr(a)
			if err != nil {
				return v, err
			}
			vs = append(vs, v)
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
