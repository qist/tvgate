package phpgo

import (
	"testing"
	"time"
)

// pregRef 构造一个 &$var 引用，键名固定为 matches。
func pregRef() Value {
	return Value{Kind: KindRef, Ref: &varRef{name: "matches"}}
}

// pregRead 从 env.vars["matches"] 读回 writeRef 写入的结果。
func pregRead(env *Env) Value {
	if v, ok := env.vars["matches"]; ok {
		return v
	}
	return NewNull()
}

// TestPregMatchLookaround 验证 preg_match 原生支持 lookbehind/lookahead
// （旧 RE2 内核会直接编译失败）。
func TestPregMatchLookaround(t *testing.T) {
	env := NewEnv(nil)
	// lookahead：取后缀前的部分
	n, _ := pregBuiltinMatch(env, []Value{
		NewString(`/zoneoffset.*?(?=accountinfo)/`),
		NewString(`zoneoffset=480accountinfo=1`),
		pregRef(),
	})
	if n.ToString() != "1" {
		t.Fatalf("lookahead: n=%v", n.ToString())
	}
	if pregRead(env).Arr["0"].ToString() != "zoneoffset=480" {
		t.Fatalf("lookahead: m0=%v", pregRead(env).Arr["0"].ToString())
	}
	// lookbehind：取前缀后的部分
	env2 := NewEnv(nil)
	n2, _ := pregBuiltinMatch(env2, []Value{
		NewString(`/(?<=\/)[^\/.]+(?=\.m3u8)/`),
		NewString("http://x/abc.m3u8"),
		pregRef(),
	})
	if n2.ToString() != "1" {
		t.Fatalf("lookbehind: n=%v", n2.ToString())
	}
	if pregRead(env2).Arr["0"].ToString() != "abc" {
		t.Fatalf("lookbehind: m0=%v", pregRead(env2).Arr["0"].ToString())
	}
}

// TestPregMatchBackref 验证回溯引用 \1（PCRE 特性，RE2 不支持）。
func TestPregMatchBackref(t *testing.T) {
	env := NewEnv(nil)
	n, _ := pregBuiltinMatch(env, []Value{
		NewString(`/(\w+) \1/`),
		NewString("hello hello world"),
		pregRef(),
	})
	if n.ToString() != "1" {
		t.Fatalf("backref: n=%v", n.ToString())
	}
	if pregRead(env).Arr["0"].ToString() != "hello hello" {
		t.Fatalf("backref: m0=%v", pregRead(env).Arr["0"].ToString())
	}
}

// TestPregMatchNamedGroups 验证 PCRE (?<name>) 命名组（.NET 语义，regexp2 原生支持）。
// 注：PHP 的 (?P<name>) 是 PCRE 语法，.NET 引擎用 (?<name>)；我们在编译前自动转换。
func TestPregMatchNamedGroups(t *testing.T) {
	// (?P<name>...) 写法（PHP 兼容，需转换）
	env := NewEnv(nil)
	n, _ := pregBuiltinMatch(env, []Value{
		NewString(`/(?P<host>[\d.]+):\d+/`),
		NewString("connect 192.0.2.70:8000 now"),
		pregRef(),
	})
	if n.ToString() != "1" {
		t.Fatalf("(?P<>): no match")
	}
	if pregRead(env).Arr["host"].ToString() != "192.0.2.70" {
		t.Fatalf("(?P<>): host=%v", pregRead(env).Arr["host"].ToString())
	}
	// (?<name>...) 写法（.NET 原生）
	env2 := NewEnv(nil)
	n2, _ := pregBuiltinMatch(env2, []Value{
		NewString(`/(?<host>[\d.]+):\d+/`),
		NewString("connect 192.0.2.70:8000 now"),
		pregRef(),
	})
	if n2.ToString() != "1" {
		t.Fatalf("(?< >): no match")
	}
	if pregRead(env2).Arr["host"].ToString() != "192.0.2.70" {
		t.Fatalf("(?< >): host=%v", pregRead(env2).Arr["host"].ToString())
	}
}

// TestPregMatchAllPatternOrder 验证默认 PREG_PATTERN_ORDER 形状
// （tx.php 的 count($m[1]) 依赖此语义；旧内核误用 SET_ORDER 形状）。
func TestPregMatchAllPatternOrder(t *testing.T) {
	env := NewEnv(nil)
	n, _ := pregBuiltinMatchAll(env, []Value{
		NewString(`/#EXTINF:([\d.]+),/`),
		NewString("#EXTINF:5.760,\n#EXTINF:6.000,\n#EXTINF:4.920,"),
		pregRef(),
	})
	if n.ToString() != "3" {
		t.Fatalf("count=%v", n.ToString())
	}
	m := pregRead(env)
	// $m[1] 应是组 1 的所有捕获（长度 3），而非第一个匹配的组 1
	grp := m.Arr["1"]
	if grp.Kind != KindArray || len(grp.Keys) != 3 {
		t.Fatalf("$m[1] should be array of 3 captures, got kind=%v len=%d", grp.Kind, len(grp.Keys))
	}
	if grp.Arr["0"].ToString() != "5.760" || grp.Arr["2"].ToString() != "4.920" {
		t.Fatalf("$m[1] captures wrong: %v %v", grp.Arr["0"].ToString(), grp.Arr["2"].ToString())
	}
}

// TestPregMatchAllSetOrder 验证 PREG_SET_ORDER 形状（外键为匹配序）。
func TestPregMatchAllSetOrder(t *testing.T) {
	env := NewEnv(nil)
	_, _ = pregBuiltinMatchAll(env, []Value{
		NewString(`/(\d+)-(\d+)/`),
		NewString("a1-2b3-4"),
		pregRef(),
		NewInt(2), // PREG_SET_ORDER
	})
	m := pregRead(env)
	if len(m.Keys) != 2 {
		t.Fatalf("SET_ORDER: expected 2 matches, got %d", len(m.Keys))
	}
	if m.Arr["0"].Arr["1"].ToString() != "1" || m.Arr["1"].Arr["2"].ToString() != "4" {
		t.Fatalf("SET_ORDER captures wrong")
	}
}

// TestPregMatchOffsetCapture 验证 PREG_OFFSET_CAPTURE。
func TestPregMatchOffsetCapture(t *testing.T) {
	env := NewEnv(nil)
	n, _ := pregBuiltinMatch(env, []Value{
		NewString(`/bcd/`),
		NewString("abcdefg"),
		pregRef(),
		NewInt(256), // PREG_OFFSET_CAPTURE
	})
	if n.ToString() != "1" {
		t.Fatalf("offset: n=%v", n.ToString())
	}
	off := pregRead(env).Arr["0"].Arr["1"].ToString()
	if off != "1" {
		t.Fatalf("offset capture: m0[1]=%v", off)
	}
}

// TestPregReplaceLimitCount 验证 preg_replace 的 limit 与 &$count。
func TestPregReplaceLimitCount(t *testing.T) {
	count := NewInt(0)
	countRef := Value{Kind: KindRef, Ref: &varRef{name: "cnt"}}
	env := NewEnv(nil)
	env.vars["cnt"] = count
	got, _ := pregBuiltinReplace(env, []Value{
		NewString(`/a/`),
		NewString("x"),
		NewString("banana"),
		NewInt(2),
		countRef,
	})
	if got.ToString() != "bxnxna" {
		t.Fatalf("limit replace: got %v", got.ToString())
	}
	if env.vars["cnt"].ToString() != "2" {
		t.Fatalf("count: got %v", env.vars["cnt"].ToString())
	}
}

// TestPregReplaceGroupRefs 验证替换串的 \1 与 $1 与 ${name} 三种引用。
func TestPregReplaceGroupRefs(t *testing.T) {
	cases := []struct{ pat, repl, subj, want string }{
		{`/(\w+)@example\.com/`, `$1`, "mail to bob@example.com", "mail to bob"},
		{`/(\w+)@example\.com/`, `\1`, "mail to bob@example.com", "mail to bob"},
		{`/(?<user>\w+)@example\.com/`, `${user}`, "mail to bob@example.com", "mail to bob"},
		{`/\$\{(\w+)\}/`, `<$1>`, "value ${name} here", "value <name> here"},
	}
	for i, c := range cases {
		got, _ := pregBuiltinReplace(nil, []Value{
			NewString(c.pat), NewString(c.repl), NewString(c.subj),
		})
		if got.ToString() != c.want {
			t.Fatalf("case %d: got %q want %q", i, got.ToString(), c.want)
		}
	}
}

// TestPregSplit 验证 preg_split 的 limit、PREG_SPLIT_NO_EMPTY 与捕获组保留。
func TestPregSplit(t *testing.T) {
	// limit：剩余并入最后元素
	r, _ := pregBuiltinSplit(nil, []Value{
		NewString(`/,/`), NewString("a,b,c,d"), NewInt(2),
	})
	if len(r.Keys) != 2 || r.Arr["1"].ToString() != "b,c,d" {
		t.Fatalf("split limit: len=%d m1=%v", len(r.Keys), r.Arr["1"].ToString())
	}
	// NO_EMPTY
	r2, _ := pregBuiltinSplit(nil, []Value{
		NewString(`/,/`), NewString("a,,b"), NewInt(-1), NewInt(1),
	})
	if len(r2.Keys) != 2 {
		t.Fatalf("split no_empty: len=%d", len(r2.Keys))
	}
	// DELIM_CAPTURE
	r3, _ := pregBuiltinSplit(nil, []Value{
		NewString(`/(,)/`), NewString("a,b"), NewInt(-1), NewInt(2),
	})
	if len(r3.Keys) != 3 || r3.Arr["1"].ToString() != "," {
		t.Fatalf("split delim_capture: len=%d m1=%v", len(r3.Keys), r3.Arr["1"].ToString())
	}
}

// TestPregGrepInvert 验证 preg_grep 的 PREG_GREP_INVERT。
func TestPregGrepInvert(t *testing.T) {
	in := NewArray()
	in.ArraySet(NewInt(0), NewString("http://a.m3u8"))
	in.ArraySet(NewInt(1), NewString("rtmp://b"))
	in.ArraySet(NewInt(2), NewString("http://c.m3u8"))
	r, _ := pregBuiltinGrep(nil, []Value{
		NewString(`/\.m3u8$/`), in, NewInt(1), // PREG_GREP_INVERT
	})
	if len(r.Keys) != 1 || r.Arr["1"].ToString() != "rtmp://b" {
		t.Fatalf("grep invert: got %d keys", len(r.Keys))
	}
}

// TestPregDelimiterHash 验证非斜杠定界符（#...#）与修饰符。
func TestPregDelimiterHash(t *testing.T) {
	env := NewEnv(nil)
	n, _ := pregBuiltinMatch(env, []Value{
		NewString(`#(\w+\.m3u8)#i`),
		NewString("PLAY https://x/live.m3u8 NOW"),
		pregRef(),
	})
	if n.ToString() != "1" {
		t.Fatalf("hash delim: n=%v", n.ToString())
	}
	if pregRead(env).Arr["1"].ToString() != "live.m3u8" {
		t.Fatalf("hash delim: m1=%v", pregRead(env).Arr["1"].ToString())
	}
}

// TestPregBacktrackTimeout 验证灾难性回溯被 500ms 超时拦截，
// 不会拖死解释器（phpgo 运行不可信脚本的安全底线）。
func TestPregBacktrackTimeout(t *testing.T) {
	start := time.Now()
	n, _ := pregBuiltinMatch(nil, []Value{
		NewString(`/(a+)+$/`),
		NewString("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaab"),
	})
	elapsed := time.Since(start)
	if n.ToString() != "0" {
		t.Fatalf("catastrophic pattern should not match, got %v", n.ToString())
	}
	if elapsed > 2*time.Second {
		t.Fatalf("timeout guard failed: took %v", elapsed)
	}
}

// TestPregConstantRegistration 验证 PREG_* 常量已进入全局常量表。
func TestPregConstantRegistration(t *testing.T) {
	env := NewEnv(nil)
	for _, name := range []string{
		"PREG_PATTERN_ORDER", "PREG_SET_ORDER", "PREG_OFFSET_CAPTURE",
		"PREG_UNMATCHED_AS_NULL", "PREG_SPLIT_NO_EMPTY", "PREG_SPLIT_DELIM_CAPTURE",
		"PREG_SPLIT_OFFSET_CAPTURE", "PREG_GREP_INVERT",
	} {
		if v, ok := env.consts[name]; !ok || v.Kind == KindNull {
			t.Fatalf("constant %s not defined", name)
		}
	}
}
