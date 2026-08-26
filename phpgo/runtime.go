package phpgo

import (
	"sort"
	"strings"
)

// Execute 解析并执行 PHP 源码（PoC 入口）。
// proxy 为 Go 实现的 HTTP 后端（替代 curl + 代理）。
// setup 在 Run 之前调用，用于设置 $_GET/$_SERVER 等。
// 返回执行后的环境（含 echo 输出、捕获的 header、退出标记）。
func Execute(src string, proxy ProxyFunc, setup func(*Env)) (*Env, error) {
	lex := NewLexer(src)
	toks, err := lex.Tokenize()
	if err != nil {
		return nil, err
	}
	p := NewParser(toks)
	prog, err := p.Parse()
	if err != nil {
		return nil, err
	}
	env := NewEnv(proxy)
	if setup != nil {
		setup(env)
	}
	if _, err := env.Run(prog); err != nil {
		return env, err
	}
	return env, nil
}

// EchoOutput 返回 echo 累积输出
func (e *Env) EchoOutput() string { return e.echoOut.String() }

// Headers 返回捕获的 header 列表
func (e *Env) Headers() []string { return e.headers }

// ExitCalled 是否触发 exit
func (e *Env) ExitCalled() bool { return e.exitLoc }

// sortStrings 升序排序（用于数组索引重建）
func sortStrings(s []string) {
	sort.Slice(s, func(i, j int) bool {
		return s[i] < s[j]
	})
}

// hasPrefix helper（备用）
func hasPrefix(s, p string) bool { return strings.HasPrefix(s, p) }
