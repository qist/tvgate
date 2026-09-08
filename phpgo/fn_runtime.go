package phpgo

// 运行时信息 / 杂项：
// version_compare / phpversion / php_sapi_name(已有) / extension_loaded / memory_get_usage /
// json_validate / register_shutdown_function / get_defined_functions。

import (
	"encoding/json"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// phpgoVersion 运行时报告给脚本的 PHP 版本（语法/函数以 8.1 语义为主）。
const phpgoVersion = "8.1.24-phpgo"

func init() {
	// version_compare($v1, $v2[, $operator])：无 operator 返回 -1/0/1
	builtins["version_compare"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		cmp := phpVersionCmp(a[0].ToString(), a[1].ToString())
		if len(a) < 3 {
			return NewInt(int64(cmp)), nil
		}
		op := strings.ToLower(strings.TrimSpace(a[2].ToString()))
		var ok bool
		switch op {
		case "<", "lt":
			ok = cmp < 0
		case "<=", "le":
			ok = cmp <= 0
		case ">", "gt":
			ok = cmp > 0
		case ">=", "ge":
			ok = cmp >= 0
		case "==", "=", "eq":
			ok = cmp == 0
		case "!=", "<>", "ne":
			ok = cmp != 0
		}
		return NewBool(ok), nil
	}
	// phpversion([$extension])
	builtins["phpversion"] = func(e *Env, a []Value) (Value, error) {
		if len(a) >= 1 {
			// 返回扩展版本；无法细分，返回 false（表示非独立扩展）
			return NewBool(false), nil
		}
		return NewString(phpgoVersion), nil
	}
	// extension_loaded($name)：判断运行时可用的扩展
	builtins["extension_loaded"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		ext := strings.ToLower(a[0].ToString())
		switch ext {
		case "standard", "core", "date", "curl", "hash", "json", "mbstring",
			"openssl", "pcre", "session", "spl", "random":
			return NewBool(true), nil
		case "mysqli", "pdo", "pdo_mysql", "mysqlnd", "gd", "xml", "simplexml",
			"dom", "xmlreader", "xmlwriter", "libxml", "redis", "memcached", "apcu":
			return NewBool(false), nil
		}
		return NewBool(false), nil
	}
	// memory_get_usage([$real_usage = false])
	builtins["memory_get_usage"] = func(e *Env, a []Value) (Value, error) {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return NewInt(int64(ms.Alloc)), nil
	}
	// json_validate($json[, $depth = 512])
	builtins["json_validate"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		return NewBool(json.Valid([]byte(a[0].ToString()))), nil
	}
	// register_shutdown_function($callback, ...$args)：脚本结束时（含 exit）按注册序执行
	builtins["register_shutdown_function"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewNull(), nil
		}
		e.shutdownFuncs = append(e.shutdownFuncs, a[0])
		return NewNull(), nil
	}
	// get_defined_functions()：返回 ['internal' => [...], 'user' => [...]]
	builtins["get_defined_functions"] = func(e *Env, a []Value) (Value, error) {
		internal := make([]string, 0, len(builtins))
		for name := range builtins {
			internal = append(internal, name)
		}
		sort.Strings(internal)
		user := make([]string, 0, len(e.funcs))
		for name := range e.funcs {
			user = append(user, name)
		}
		sort.Strings(user)
		internalArr := NewArray()
		for _, n := range internal {
			internalArr.ArraySet(NewInt(int64(len(internalArr.Keys))), NewString(n))
		}
		userArr := NewArray()
		for _, n := range user {
			userArr.ArraySet(NewInt(int64(len(userArr.Keys))), NewString(n))
		}
		out := NewArray()
		out.ArraySet(NewString("internal"), internalArr)
		out.ArraySet(NewString("user"), userArr)
		return out, nil
	}
}

// vTok 版本号解析出的一个 token：数字或限定词
type vTok struct {
	num  bool
	val  int64
	word string
}

// phpVersionTokens 把版本串拆成 token 序列（忽略分隔符，数字/字母各自成段）
func phpVersionTokens(v string) []vTok {
	v = strings.ToLower(strings.TrimSpace(v))
	if len(v) > 1 && v[0] == 'v' && v[1] >= '0' && v[1] <= '9' {
		v = v[1:]
	}
	var toks []vTok
	i := 0
	for i < len(v) {
		c := v[i]
		if !(c >= 'a' && c <= 'z') && !(c >= '0' && c <= '9') {
			i++
			continue
		}
		if c >= '0' && c <= '9' {
			j := i
			for j < len(v) && v[j] >= '0' && v[j] <= '9' {
				j++
			}
			n, _ := strconv.ParseInt(v[i:j], 10, 64)
			toks = append(toks, vTok{num: true, val: n})
			i = j
			continue
		}
		j := i
		for j < len(v) && v[j] >= 'a' && v[j] <= 'z' {
			j++
		}
		toks = append(toks, vTok{word: v[i:j]})
		i = j
	}
	return toks
}

// wordRank 限定词排序：dev < alpha/a < beta/b < RC/rc < 稳定(4) < pl/p
func wordRank(w string) int {
	switch w {
	case "dev":
		return 0
	case "alpha", "a":
		return 1
	case "beta", "b":
		return 2
	case "rc":
		return 3
	case "pl", "p":
		return 5
	}
	// 未知限定词按稳定级处理（避免误判）
	return 4
}

// cmpVersionToken 比较单个 token；缺少方以稳定版填充（number 0）
func cmpVersionToken(a, b *vTok) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return cmpVersionToken(&vTok{num: true, val: 0}, b)
	}
	if b == nil {
		return cmpVersionToken(a, &vTok{num: true, val: 0})
	}
	if a.num && b.num {
		if a.val < b.val {
			return -1
		}
		if a.val > b.val {
			return 1
		}
		return 0
	}
	// 数值 vs 限定词：数值视作稳定级（rank 4），基础数值 0 与词比较
	if a.num {
		if b.num {
			return 0
		}
		if a.val != 0 {
			return 1 // 有数值后缀的版本高于纯词（如 1 > rc）
		}
		return cmpStableWord(b.word)
	}
	if b.num {
		if b.val != 0 {
			return -1
		}
		return -cmpStableWord(a.word)
	}
	// 两个词
	r1, r2 := wordRank(a.word), wordRank(b.word)
	if r1 != r2 {
		if r1 < r2 {
			return -1
		}
		return 1
	}
	if a.word == b.word {
		return 0
	}
	if r1 == 4 && r2 == 4 {
		return strings.Compare(a.word, b.word)
	}
	// 同 rank 的别名（a/alpha、b/beta、p/pl 等）：按字节序近似
	return strings.Compare(a.word, b.word)
}

// cmpStableWord 稳定级（数值 0）vs 词
func cmpStableWord(w string) int {
	r := wordRank(w)
	if r == 4 {
		return 0
	}
	if r < 4 {
		return 1 // 稳定 > 预发布
	}
	return -1 // 稳定 < pl/p
}

// phpVersionCmp 返回 <0 / 0 / >0
func phpVersionCmp(a, b string) int {
	ta, tb := phpVersionTokens(a), phpVersionTokens(b)
	n := len(ta)
	if len(tb) > n {
		n = len(tb)
	}
	for i := 0; i < n; i++ {
		var pa, pb *vTok
		if i < len(ta) {
			pa = &ta[i]
		}
		if i < len(tb) {
			pb = &tb[i]
		}
		if c := cmpVersionToken(pa, pb); c != 0 {
			return c
		}
	}
	return 0
}
