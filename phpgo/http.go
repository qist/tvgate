package phpgo

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
)

// ServePHP 执行 PHP 源码并写入 HTTP 响应。
// src 为脚本源码；req 可为 nil（命令行/测试用）。
func ServePHP(env *Env, w http.ResponseWriter, src string) error {
	// 重置输出缓冲
	env.echoOut.Reset()
	env.headers = nil
	env.exitLoc = false

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
			w.Header().Set(strings.TrimSpace(h[:i]), strings.TrimSpace(h[i+1:]))
		}
	}
	// 未显式设置 Content-Type 时默认 text/html
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, env.echoOut.String())
	return nil
}

// defaultProxy 是 ProxyFunc 的默认实现：用 Go 标准库发请求（支持代理）。
// 由外部注入 *http.Client（含代理 transport）。
func defaultProxy(client *http.Client) ProxyFunc {
	return func(method, u string, opts *CurlOptions) (string, error) {
		if client == nil {
			client = http.DefaultClient
		}
		var body io.Reader
		if opts != nil && opts.PostData != "" {
			body = strings.NewReader(opts.PostData)
		}
		req, err := http.NewRequest(method, u, body)
		if err != nil {
			return "", err
		}
		if opts != nil {
			for _, h := range opts.Headers {
				if i := strings.IndexByte(h, ':'); i > 0 {
					req.Header.Set(strings.TrimSpace(h[:i]), strings.TrimSpace(h[i+1:]))
				}
			}
			if opts.UserAgent != "" {
				req.Header.Set("User-Agent", opts.UserAgent)
			}
		}
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
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
		body, err := env.proxy("GET", path, &CurlOptions{})
		if err != nil {
			return NewBool(false), err
		}
		return NewString(body), nil
	}
	if path == "php://input" {
		return NewString(env.phpInput), nil
	}
	data, err := os_ReadFile(path)
	if err != nil {
		return NewBool(false), err
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
//   原 pattern = [prefix] (?<=LIT) MAIN (?=TAIL) [suffix]
//   改写为     = [prefix] (LIT) (MAIN) (TAIL) [suffix]
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
		lit = rest[:end] // 断言内的字面量（可能含转义 \/）
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
		tail = rest[:end] // 断言内的字面量
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
}

// varRef 表示一个变量引用（用于 preg_match 的引用参数）
type varRef struct {
	name string
}

func (r *varRef) assign(env *Env, v Value) {
	env.vars[r.name] = v
	env.globals[r.name] = v
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
	if len(pat) >= 2 {
		delim := pat[0]
		if (delim == '/' || delim == '#' || delim == '~' || delim == '!') && pat[len(pat)-1] == delim {
			// 去掉首尾定界符及修饰符（如 /.../i 的 i）
			inner := pat[1:]
			if i := strings.LastIndexByte(inner, delim); i >= 0 {
				inner = inner[:i]
			}
			pat = inner
		}
	}
	// PCRE 修饰符 i（不区分大小写）转 Go 内联 (?i)
	// 这里简单处理：若原串带 /i，已在上面剥离，用 (?i) 包裹
	pat = strings.TrimPrefix(pat, "(?i)")
	re, err := regexp.Compile("(?i)" + pat)
	if err != nil {
		// 失败则尝试原样
		return regexp.Compile(pat)
	}
	return re, nil
}
