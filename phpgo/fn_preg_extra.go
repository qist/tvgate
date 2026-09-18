package phpgo

import "strings"

// preg_quote：转义正则特殊字符（可选指定定界符一并转义）
// （preg_match/match_all/replace/split/grep 见 fn_preg_core.go）
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
}
