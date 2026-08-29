package phpgo

import (
	"regexp"
	"strings"
)

// 供 strip_tags 使用的标签匹配（含 script/style 整段内容）
var stripTagRe = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>|<[^>]+>`)

// rot13 对字母做 ROT13 旋转
func rot13(s string) string {
	b := []byte(s)
	for i, c := range b {
		switch {
		case c >= 'a' && c <= 'z':
			b[i] = 'a' + (c-'a'+13)%26
		case c >= 'A' && c <= 'Z':
			b[i] = 'A' + (c-'A'+13)%26
		}
	}
	return string(b)
}

func init() {
	builtins["strrev"] = func(e *Env, a []Value) (Value, error) {
		b := []byte(a[0].ToString())
		for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
			b[i], b[j] = b[j], b[i]
		}
		return NewString(string(b)), nil
	}
	builtins["str_shuffle"] = func(e *Env, a []Value) (Value, error) {
		b := []byte(a[0].ToString())
		for i := len(b) - 1; i > 0; i-- {
			j := cryptoRandIntn(i + 1)
			b[i], b[j] = b[j], b[i]
		}
		return NewString(string(b)), nil
	}
	builtins["str_rot13"] = func(e *Env, a []Value) (Value, error) {
		return NewString(rot13(a[0].ToString())), nil
	}
	// substr_count：统计 needle 在 haystack 中的非重叠出现次数
	builtins["substr_count"] = func(e *Env, a []Value) (Value, error) {
		hay := a[0].ToString()
		needle := a[1].ToString()
		if needle == "" {
			return NewInt(0), nil
		}
		return NewInt(int64(strings.Count(hay, needle))), nil
	}
	// substr_replace：替换指定位置的子串
	builtins["substr_replace"] = func(e *Env, a []Value) (Value, error) {
		s := a[0].ToString()
		repl := a[1].ToString()
		start := a[2].ToInt()
		length := int64(-1)
		if len(a) >= 4 {
			length = a[3].ToInt()
		}
		if start < 0 {
			start = int64(len(s)) + start
		}
		if start < 0 {
			start = 0
		}
		if start > int64(len(s)) {
			start = int64(len(s))
		}
		end := int64(len(s))
		if length >= 0 {
			end = start + length
		} else if length < 0 && len(a) >= 4 {
			end = int64(len(s)) + length
		}
		if end < start {
			end = start
		}
		if end > int64(len(s)) {
			end = int64(len(s))
		}
		return NewString(s[:start] + repl + s[end:]), nil
	}
	// strip_tags：去除 HTML/XML 标签（含 script/style 内容）
	builtins["strip_tags"] = func(e *Env, a []Value) (Value, error) {
		s := stripTagRe.ReplaceAllString(a[0].ToString(), "")
		return NewString(s), nil
	}
	// str_word_count：统计单词数；format=1 返回单词数组，format=2 返回带偏移数组
	builtins["str_word_count"] = func(e *Env, a []Value) (Value, error) {
		s := a[0].ToString()
		format := 0
		if len(a) >= 2 {
			format = int(a[1].ToInt())
		}
		words := []string{}
		offsets := []int{}
		for i := 0; i < len(s); {
			// 跳过非字母/数字/下划线
			for i < len(s) && !isWordChar(s[i]) {
				i++
			}
			start := i
			for i < len(s) && isWordChar(s[i]) {
				i++
			}
			if i > start {
				words = append(words, s[start:i])
				offsets = append(offsets, start)
			}
		}
		if format == 0 {
			return NewInt(int64(len(words))), nil
		}
		arr := NewArray()
		if format == 1 {
			for _, w := range words {
				arr.ArraySet(NewInt(int64(len(arr.Keys))), NewString(w))
			}
		} else { // format 2：key 为偏移
			for i, w := range words {
				arr.ArraySet(NewInt(int64(offsets[i])), NewString(w))
			}
		}
		return arr, nil
	}
}

func isWordChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}
