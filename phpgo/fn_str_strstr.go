package phpgo

import "strings"

func init() {
	builtins["strstr"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		s := a[0].ToString()
		sub := a[1].ToString()
		idx := strings.Index(s, sub)
		if idx < 0 {
			return NewBool(false), nil
		}
		if len(a) >= 3 && a[2].ToBool() {
			return NewString(s[:idx]), nil
		}
		return NewString(s[idx:]), nil
	}
	// strchr 是 strstr 的别名
	builtins["strchr"] = builtins["strstr"]
}
