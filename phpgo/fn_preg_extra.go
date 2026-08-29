package phpgo

import "strings"

// preg_quote：转义正则特殊字符（可选指定定界符一并转义）
func init() {
	builtins["preg_quote"] = func(e *Env, a []Value) (Value, error) {
		s := a[0].ToString()
		delim := ""
		if len(a) >= 2 {
			delim = a[1].ToString()
		}
		special := ".\\+*?[^]$(){}=!<>|:-#" + delim
		var b strings.Builder
		for i := 0; i < len(s); i++ {
			if strings.IndexByte(special, s[i]) >= 0 {
				b.WriteByte('\\')
			}
			b.WriteByte(s[i])
		}
		return NewString(b.String()), nil
	}
	// preg_grep：返回数组中值匹配模式的元素（保留原键）
	builtins["preg_grep"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 || a[1].Kind != KindArray {
			return NewArray(), nil
		}
		re, err := compilePHPRegex(a[0].ToString())
		if err != nil {
			return NewArray(), nil
		}
		invert := false
		if len(a) >= 3 {
			invert = a[2].ToBool()
		}
		result := NewArray()
		for _, k := range a[1].Keys {
			match := re.MatchString(a[1].Arr[k].ToString())
			if invert {
				match = !match
			}
			if match {
				result.ArraySet(NewString(k), a[1].Arr[k])
			}
		}
		return result, nil
	}
}
