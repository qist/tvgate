package phpgo

// phpgo PCRE 内核：PHP 定界符/修饰符外壳 + regexp2 回溯引擎。
//
// 旧内核基于 Go stdlib RE2，不支持 lookbehind/lookahead、回溯引用、
// 占有量词、条件表达式，靠两个字符串级特例 workaround 硬扛 fj.php 等
// 个别脚本。本文件改用 regexp2（.NET 语义回溯正则）统一实现全部 PCRE
// 函数，PHP 语义对齐要点：
//
//   - 定界符：非字母数字/反斜杠/空白的首字符；括号类 () [] {} <> 按配对闭符
//   - 修饰符：i/m/s/x 映射 .NET 选项；u/e/A/S/D/J/U 忽略（U 反转贪婪无对应项，
//     .NET x 模式在字符类内也忽略空白，与 PCRE 有细微差异，解析脚本场景无碍）
//   - 替换串：\1..9、$1..9、${name} 手动展开（不依赖 .NET $ 替换语法）
//   - preg_match_all 默认 PATTERN_ORDER（旧内核误用 SET_ORDER 形状，tx.php 的
//     count($m[1]) 实际是坏的，本次修正）
//   - 补齐 offset 参数、PREG_OFFSET_CAPTURE、PREG_UNMATCHED_AS_NULL、
//     preg_replace/split 的 limit 与 &$count
//   - 残余不支持：(?R) 递归、\K——regexp2 编译报错，按 PHP 语义返回 false/null
//   - 回溯引擎必须设超时：phpgo 执行不可信 php:// 脚本，防灾难性回溯拖死解释器

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/dlclark/regexp2"
)

// pregTimeout 单次匹配的回溯预算上限。
const pregTimeout = 500 * time.Millisecond

// errPregTimeout 匹配超时（映射 PHP 的 PREG_BACKTRACK_LIMIT_ERROR）。
// regexp2 的超时错误没有导出哨兵值，靠消息前缀识别。
var errPregTimeout = errors.New("preg: backtrack limit exceeded")

// phpRegex 是编译后的 PCRE 模式：定界符已剥离、修饰符已落到 regexp2 选项。
type phpRegex struct {
	re   *regexp2.Regexp
	mods string // 原始修饰符串（保留以便扩展）
}

// compilePHPRegex 解析 PHP PCRE 表达式并编译为 regexp2。
// 无定界符形态（如直接传 "(?<=x)y"）原样编译，与 PHP 传参习惯兼容。
func compilePHPRegex(pat string) (*phpRegex, error) {
	inner, mods := stripPCREDelims(pat)
	var opts regexp2.RegexOptions
	for _, ch := range mods {
		switch ch {
		case 'i':
			opts |= regexp2.IgnoreCase
		case 'm':
			opts |= regexp2.Multiline
		case 's':
			opts |= regexp2.Singleline
		case 'x':
			opts |= regexp2.IgnorePatternWhitespace
		}
	}
	re, err := regexp2.Compile(translatePCREGroups(inner), opts)
	if err != nil {
		return nil, err
	}
	re.MatchTimeout = pregTimeout
	return &phpRegex{re: re, mods: mods}, nil
}

// translatePCREGroups 把 PHP 的 (?P<name>) 命名组写法转为 .NET 的 (?<name>)。
// 仅命中 "?P<" 序列，误伤概率可忽略（字面字符串需写成 \(\?P< 才会进模式）。
func translatePCREGroups(p string) string {
	if !strings.Contains(p, "?P<") {
		return p
	}
	return strings.ReplaceAll(p, "?P<", "?<")
}

// stripPCREDelims 剥离 PCRE 定界符，返回模式主体与修饰符串。
//
// 非配对定界符（/#~! 等）：主体内首个未转义定界符即结束，其后必须是
// 纯字母修饰符串，否则视为无定界符模式（宽松于 PCRE 的报错行为）。
//
// 配对定界符（() [] {} <>）：仅当整个模式呈「(主体)修饰符」包裹形态时
// 才剥离——从尾部剥修饰符后末字符须恰为闭符、且主体内没有未转义闭符。
// 这样无定界符传参（如 "(?<=x)y" 以 ( 开头）不会被误剥。
func stripPCREDelims(pat string) (string, string) {
	if len(pat) < 2 {
		return pat, ""
	}
	d := pat[0]
	if d == '\\' || isPregWordByte(d) || isPregSpaceByte(d) {
		return pat, ""
	}
	var close byte
	paired := false
	switch d {
	case '(':
		close, paired = ')', true
	case '[':
		close, paired = ']', true
	case '{':
		close, paired = '}', true
	case '<':
		close, paired = '>', true
	default:
		close = d
	}
	if !paired {
		for i := 1; i < len(pat); i++ {
			c := pat[i]
			if c == '\\' {
				i++ // 跳过被转义的字节
				continue
			}
			if c == close {
				mods := pat[i+1:]
				if !isModifierRun(mods) {
					return pat, ""
				}
				return pat[1:i], mods
			}
		}
		return pat, ""
	}
	// 配对定界符：从尾部剥掉修饰符字母，要求末字符恰为配对闭符
	end := len(pat) - 1
	for end >= 1 && isPregWordByte(pat[end]) {
		end-- // 修饰符只能是字母
	}
	if end < 1 || pat[end] != close {
		return pat, ""
	}
	for i := 1; i < end; i++ {
		if pat[i] == '\\' {
			i++
			continue
		}
		if pat[i] == close {
			return pat, "" // 主体含裸闭符 → 不是定界符包裹形态
		}
	}
	return pat[1:end], pat[end+1:]
}

func isPregWordByte(b byte) bool {
	return b == '_' ||
		(b >= '0' && b <= '9') ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z')
}

func isPregSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f'
}

// isModifierRun 判断是否为合法的修饰符字母串（PCRE 修饰符集imsxeADSUXJug 等）。
func isModifierRun(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isPregWordByte(s[i]) {
			return false
		}
	}
	return true
}

// pregMatchError 把 regexp2 错误归一为 PHP 错误类语义（超时 → 回溯超限）。
func pregMatchError(err error) error {
	if err != nil && strings.HasPrefix(err.Error(), "match timeout") {
		return errPregTimeout
	}
	return err
}

// pregGroupValue 取一个捕获组的 PHP 展示值：重复捕获组取最后一次捕获
// （PCRE 语义）；未参与匹配返回空串或 null（PREG_UNMATCHED_AS_NULL）。
func pregGroupValue(m *regexp2.Match, num int, unmatchedNull bool) Value {
	g := m.GroupByNumber(num)
	if g == nil || len(g.Captures) == 0 {
		if unmatchedNull {
			return NewNull()
		}
		return NewString("")
	}
	return NewString(g.Captures[len(g.Captures)-1].String())
}

// pregMatchToPHPArray 把一次匹配展开为 PHP $matches 数组：
// 数字键 + 命名组别名键（PHP 语义 $matches['name'] 与对应数字键同值）。
// flags 支持 PREG_OFFSET_CAPTURE（元素变 [值, 字节偏移]）与
// PREG_UNMATCHED_AS_NULL（未匹配组为 null 而非空串）。
func pregMatchToPHPArray(pr *phpRegex, m *regexp2.Match, flags int) Value {
	arr := NewArray()
	unmatchedNull := flags&512 != 0
	offsetCapture := flags&256 != 0
	byName := map[string]int{}
	if pr != nil && pr.re != nil {
		names := pr.re.GetGroupNames()
		nums := pr.re.GetGroupNumbers()
		for i, n := range nums {
			if i < len(names) {
				byName[names[i]] = n
			}
		}
	}
	setGroup := func(key Value, num int) {
		v := pregGroupValue(m, num, unmatchedNull)
		if offsetCapture {
			pair := NewArray()
			pair.ArraySet(NewInt(0), v)
			pair.ArraySet(NewInt(1), NewInt(int64(m.GroupByNumber(num).Index)))
			arr.ArraySet(key, pair)
		} else {
			arr.ArraySet(key, v)
		}
	}
	for g := 0; g < m.GroupCount(); g++ {
		setGroup(NewInt(int64(g)), g)
	}
	// 命名组别名：非纯数字的组名指向对应数字键的值
	for name, num := range byName {
		if isAllDigits(name) {
			continue
		}
		if src, ok := arr.Arr[strconv.Itoa(num)]; ok {
			arr.ArraySet(NewString(name), src)
		}
	}
	return arr
}

// isAllDigits 判断组名是否为纯数字（regexp2 未命名组名为数字串）。
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// pregEachMatch 迭代 subject 的所有匹配（含空匹配前进保护），
// 由回调消费每个匹配；回调返回 false 时提前终止。
// 返回错误仅来自回溯超时。
func pregEachMatch(pr *phpRegex, subj string, startAt int, fn func(m *regexp2.Match) bool) error {
	if startAt > len(subj) {
		return nil
	}
	m, err := pr.re.FindStringMatchStartingAt(subj, startAt)
	for m != nil && err == nil {
		idx, ln := m.Index, m.Length
		if !fn(m) {
			return nil
		}
		// 空匹配：手动前进一个字节，避免死循环（.NET 语义近似）
		step := ln
		if step == 0 {
			step = 1
		}
		if idx+step > len(subj) {
			return nil
		}
		m, err = pr.re.FindStringMatchStartingAt(subj, idx+step)
	}
	return pregMatchError(err)
}

// expandPHPReplacement 按 PHP 语义展开替换串：
// \1..\9 与 $1..$9 为组引用（\0/$0 整匹配）、${name} 命名组引用、
// \\ 为字面反斜杠；$ 后非数字/花括号按字面处理，组不存在展开为空串。
func expandPHPReplacement(repl string, m *regexp2.Match) string {
	if !strings.ContainsAny(repl, `\$`) {
		return repl
	}
	var b strings.Builder
	for i := 0; i < len(repl); i++ {
		c := repl[i]
		if c == '\\' && i+1 < len(repl) {
			n := repl[i+1]
			if n >= '0' && n <= '9' {
				b.WriteString(pregGroupValue(m, int(n-'0'), false).ToString())
				i++
				continue
			}
			if n == '\\' {
				b.WriteByte('\\')
				i++
				continue
			}
			b.WriteByte('\\')
			continue
		}
		if c == '$' && i+1 < len(repl) {
			n := repl[i+1]
			if n >= '0' && n <= '9' {
				j := i + 1
				num := 0
				for j < len(repl) && repl[j] >= '0' && repl[j] <= '9' {
					num = num*10 + int(repl[j]-'0')
					j++
				}
				b.WriteString(pregGroupValue(m, num, false).ToString())
				i = j - 1
				continue
			}
			if n == '{' {
				if end := strings.IndexByte(repl[i+2:], '}'); end >= 0 {
					name := repl[i+2 : i+2+end]
					b.WriteString(pregNamedValue(m, name))
					i = i + 2 + end
					continue
				}
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}

// pregNamedValue 按名字取捕获组；名字不存在返回空串。
func pregNamedValue(m *regexp2.Match, name string) string {
	g := m.GroupByName(name)
	if g == nil || len(g.Captures) == 0 {
		return ""
	}
	return g.Captures[len(g.Captures)-1].String()
}

// pregReplaceInString 在 subj 上应用单模式替换（手动迭代实现 limit 与 count），
// 返回替换后的完整串。
func pregReplaceInString(pr *phpRegex, repl string, subj string, limit int, count *int64) (string, error) {
	var b strings.Builder
	last := 0
	done := 0
	err := pregEachMatch(pr, subj, 0, func(m *regexp2.Match) bool {
		if limit >= 0 && done >= limit {
			return false
		}
		b.WriteString(subj[last:m.Index])
		b.WriteString(expandPHPReplacement(repl, m))
		last = m.Index + m.Length
		done++
		if count != nil {
			*count++
		}
		return true
	})
	if err != nil {
		return "", err
	}
	b.WriteString(subj[last:])
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// 内置函数注册
// ---------------------------------------------------------------------------

func init() {
	builtins["preg_match"] = pregBuiltinMatch
	builtins["preg_match_all"] = pregBuiltinMatchAll
	builtins["preg_replace"] = pregBuiltinReplace
	builtins["preg_replace_callback"] = pregBuiltinReplaceCallback
	builtins["preg_replace_callback_array"] = pregBuiltinReplaceCallbackArray
	builtins["preg_split"] = pregBuiltinSplit
	builtins["preg_grep"] = pregBuiltinGrep
}

// preg_match($pattern, $subject[, &$matches[, $flags = 0[, $offset = 0]]])
// 命中返回 1、未命中返回 0、出错返回 false（PHP 语义）。
func pregBuiltinMatch(env *Env, vs []Value) (Value, error) {
	if len(vs) < 2 {
		return NewBool(false), nil
	}
	pr, err := compilePHPRegex(vs[0].ToString())
	if err != nil {
		return NewBool(false), nil
	}
	subj := vs[1].ToString()
	flags := 0
	if len(vs) >= 4 {
		flags = int(vs[3].ToInt())
	}
	offset := 0
	if len(vs) >= 5 {
		offset = int(vs[4].ToInt())
	}
	var m *regexp2.Match
	if offset > 0 {
		if offset > len(subj) {
			return NewInt(0), nil
		}
		m, err = pr.re.FindStringMatchStartingAt(subj, offset)
	} else {
		m, err = pr.re.FindStringMatch(subj)
	}
	if err = pregMatchError(err); err != nil || m == nil {
		return NewInt(0), nil
	}
	if len(vs) >= 3 {
		writeRef(env, vs[2], pregMatchToPHPArray(pr, m, flags))
	}
	return NewInt(1), nil
}

// preg_match_all($pattern, $subject[, &$matches[, $flags = PREG_PATTERN_ORDER[, $offset = 0]]])
func pregBuiltinMatchAll(env *Env, vs []Value) (Value, error) {
	if len(vs) < 2 {
		return NewInt(0), nil
	}
	pr, err := compilePHPRegex(vs[0].ToString())
	if err != nil {
		return NewInt(0), nil
	}
	subj := vs[1].ToString()
	flags := 1 // PREG_PATTERN_ORDER
	if len(vs) >= 4 {
		if f := int(vs[3].ToInt()); f != 0 {
			flags = f
		}
	}
	offset := 0
	if len(vs) >= 5 {
		offset = int(vs[4].ToInt())
	}
	offsetCapture := flags&256 != 0
	var matches []*regexp2.Match
	err = pregEachMatch(pr, subj, offset, func(m *regexp2.Match) bool {
		matches = append(matches, m)
		return true
	})
	if err != nil {
		return NewInt(0), nil
	}
	if len(vs) >= 3 {
		outer := NewArray()
		switch {
		case flags&2 != 0: // PREG_SET_ORDER：外键为匹配序
			for i, m := range matches {
				outer.ArraySet(NewInt(int64(i)), pregMatchToPHPArray(pr, m, flags))
			}
		default: // PREG_PATTERN_ORDER：外键为组号（PHP 默认）
			for g := 0; g < matchGroupCount(matches); g++ {
				grp := NewArray()
				for _, m := range matches {
					if g >= m.GroupCount() {
						continue
					}
					v := pregGroupValue(m, g, flags&512 != 0)
					if offsetCapture {
						pair := NewArray()
						pair.ArraySet(NewInt(0), v)
						pair.ArraySet(NewInt(1), NewInt(int64(m.GroupByNumber(g).Index)))
						grp.ArraySet(NewInt(int64(len(grp.Keys))), pair)
					} else {
						grp.ArraySet(NewInt(int64(len(grp.Keys))), v)
					}
				}
				outer.ArraySet(NewInt(int64(g)), grp)
			}
		}
		writeRef(env, vs[2], outer)
	}
	return NewInt(int64(len(matches))), nil
}

// matchGroupCount 返回一组匹配中的最大组数（用于 PATTERN_ORDER 展开）。
func matchGroupCount(ms []*regexp2.Match) int {
	n := 0
	for _, m := range ms {
		if m.GroupCount() > n {
			n = m.GroupCount()
		}
	}
	return n
}

// preg_valueSlice 把参数拆为字符串列表（标量 → 单元素）。
func pregValueSlice(v Value) []Value {
	if v.Kind == KindArray {
		out := make([]Value, 0, len(v.Keys))
		for _, k := range v.Keys {
			out = append(out, v.Arr[k])
		}
		return out
	}
	return []Value{v}
}

// pregBuiltinReplace 实现 preg_replace($pattern, $replacement, $subject[, $limit[, &$count]])
// pattern/replacement/subject 均支持数组（PHP 语义：模式与替换按索引配对，
// 替换不足处用空串；subject 为数组时逐个应用并返回数组）。
func pregBuiltinReplace(env *Env, vs []Value) (Value, error) {
	if len(vs) < 3 {
		return NewNull(), nil
	}
	pats := pregValueSlice(vs[0])
	repls := pregValueSlice(vs[1])
	subjs := pregValueSlice(vs[2])
	limit := int64(-1)
	if len(vs) >= 4 {
		limit = vs[3].ToInt()
	}
	var count int64
	applyOne := func(subj Value) Value {
		out := subj.ToString()
		for i, p := range pats {
			repl := ""
			if i < len(repls) {
				repl = repls[i].ToString()
			}
			pr, err := compilePHPRegex(p.ToString())
			if err != nil {
				continue // 出错的模式跳过（PHP 发 warning 后继续）
			}
			res, err := pregReplaceInString(pr, repl, out, int(limit), &count)
			if err != nil {
				continue
			}
			out = res
		}
		return NewString(out)
	}
	var result Value
	if vs[2].Kind == KindArray {
		result = NewArray()
		for _, k := range vs[2].Keys {
			result.ArraySet(NewString(k), applyOne(vs[2].Arr[k]))
		}
	} else {
		result = applyOne(subjs[0])
	}
	if len(vs) >= 5 {
		writeRef(env, vs[4], NewInt(count))
	}
	return result, nil
}

// pregBuiltinReplaceCallback 实现 preg_replace_callback($pattern, $callback, $subject[, $limit[, &$count]])
func pregBuiltinReplaceCallback(env *Env, vs []Value) (Value, error) {
	if len(vs) < 3 {
		return NewNull(), nil
	}
	pats := pregValueSlice(vs[0])
	subjs := pregValueSlice(vs[2])
	limit := int64(-1)
	if len(vs) >= 4 {
		limit = vs[3].ToInt()
	}
	var count int64
	applyOne := func(subj Value) Value {
		out := subj.ToString()
		for _, p := range pats {
			pr, err := compilePHPRegex(p.ToString())
			if err != nil {
				continue
			}
			var b strings.Builder
			last := 0
			done := 0
			_ = pregEachMatch(pr, out, 0, func(m *regexp2.Match) bool {
				if limit >= 0 && int64(done) >= limit {
					return false
				}
				b.WriteString(out[last:m.Index])
				matches := pregMatchToPHPArray(pr, m, 0)
				ret, _ := callCallable(env, vs[1], []Value{matches})
				b.WriteString(ret.ToString())
				last = m.Index + m.Length
				done++
				count++
				return true
			})
			b.WriteString(out[last:])
			out = b.String()
		}
		return NewString(out)
	}
	var result Value
	if vs[2].Kind == KindArray {
		result = NewArray()
		for _, k := range vs[2].Keys {
			result.ArraySet(NewString(k), applyOne(vs[2].Arr[k]))
		}
	} else {
		result = applyOne(subjs[0])
	}
	if len(vs) >= 5 {
		writeRef(env, vs[4], NewInt(count))
	}
	return result, nil
}

// pregBuiltinReplaceCallbackArray 实现 preg_replace_callback_array([pattern => cb, ...], $subject[, $limit[, &$count]])
func pregBuiltinReplaceCallbackArray(env *Env, vs []Value) (Value, error) {
	if len(vs) < 2 || vs[0].Kind != KindArray {
		return NewNull(), nil
	}
	cbMap := vs[0]
	limit := int64(-1)
	if len(vs) >= 3 {
		limit = vs[2].ToInt()
	}
	var count int64
	replaceOne := func(s string) string {
		out := s
		for _, k := range cbMap.Keys {
			if limit >= 0 && count >= limit {
				break
			}
			pr, err := compilePHPRegex(k)
			if err != nil {
				continue
			}
			var b strings.Builder
			last := 0
			done := 0
			_ = pregEachMatch(pr, out, 0, func(m *regexp2.Match) bool {
				if limit >= 0 && int64(done) >= limit {
					return false
				}
				b.WriteString(out[last:m.Index])
				ret, _ := callCallable(env, cbMap.Arr[k], []Value{pregMatchToPHPArray(pr, m, 0)})
				b.WriteString(ret.ToString())
				last = m.Index + m.Length
				done++
				count++
				return true
			})
			b.WriteString(out[last:])
			out = b.String()
		}
		return out
	}
	var result Value
	if vs[1].Kind == KindArray {
		result = NewArray()
		for _, k := range vs[1].Keys {
			result.ArraySet(NewString(k), NewString(replaceOne(vs[1].Arr[k].ToString())))
		}
	} else {
		result = NewString(replaceOne(vs[1].ToString()))
	}
	if len(vs) >= 4 {
		writeRef(env, vs[3], NewInt(count))
	}
	return result, nil
}

// pregBuiltinSplit 实现 preg_split($pattern, $subject[, $limit = -1[, $flags = 0]])
// flags：PREG_SPLIT_NO_EMPTY=1 / PREG_SPLIT_DELIM_CAPTURE=2 / PREG_SPLIT_OFFSET_CAPTURE=4
func pregBuiltinSplit(env *Env, vs []Value) (Value, error) {
	if len(vs) < 2 {
		return NewArray(), nil
	}
	pr, err := compilePHPRegex(vs[0].ToString())
	if err != nil {
		return NewArray(), nil
	}
	subj := vs[1].ToString()
	limit := int64(-1)
	if len(vs) >= 3 {
		limit = vs[2].ToInt()
	}
	flags := 0
	if len(vs) >= 4 {
		flags = int(vs[3].ToInt())
	}
	noEmpty := flags&1 != 0
	delimCapture := flags&2 != 0
	offsetCapture := flags&4 != 0

	type seg struct {
		text string
		off  int
	}
	var segs []seg
	emit := func(text string, off int) {
		if noEmpty && text == "" {
			return
		}
		segs = append(segs, seg{text, off})
	}
	last := 0
	done := int64(0)
	_ = pregEachMatch(pr, subj, 0, func(m *regexp2.Match) bool {
		if limit >= 0 && done >= limit-1 {
			return false // 剩余部分并入最后元素
		}
		emit(subj[last:m.Index], last)
		if delimCapture {
			for g := 1; g < m.GroupCount(); g++ {
				if cv := pregGroupValue(m, g, false).ToString(); cv != "" {
					cgroup := m.GroupByNumber(g)
					emit(cv, cgroup.Captures[len(cgroup.Captures)-1].Index)
				}
			}
		}
		last = m.Index + m.Length
		done++
		return true
	})
	emit(subj[last:], last)

	result := NewArray()
	for i, s := range segs {
		if offsetCapture {
			pair := NewArray()
			pair.ArraySet(NewInt(0), NewString(s.text))
			pair.ArraySet(NewInt(1), NewInt(int64(s.off)))
			result.ArraySet(NewInt(int64(i)), pair)
		} else {
			result.ArraySet(NewInt(int64(i)), NewString(s.text))
		}
	}
	return result, nil
}

// pregBuiltinGrep 实现 preg_grep($pattern, $input[, $flags = 0])
// flags：PREG_GREP_INVERT=1
func pregBuiltinGrep(env *Env, vs []Value) (Value, error) {
	if len(vs) < 2 || vs[1].Kind != KindArray {
		return NewArray(), nil
	}
	pr, err := compilePHPRegex(vs[0].ToString())
	if err != nil {
		return NewArray(), nil
	}
	invert := len(vs) >= 3 && vs[2].ToInt() != 0
	result := NewArray()
	for _, k := range vs[1].Keys {
		match, _ := pr.re.MatchString(vs[1].Arr[k].ToString())
		if invert {
			match = !match
		}
		if match {
			result.ArraySet(NewString(k), vs[1].Arr[k])
		}
	}
	return result, nil
}
