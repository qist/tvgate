package phpgo

import "strings"

func init() {
	builtins["htmlspecialchars"] = func(e *Env, a []Value) (Value, error) {
		s := a[0].ToString()
		s = strings.ReplaceAll(s, "&", "&amp;")
		s = strings.ReplaceAll(s, "<", "&lt;")
		s = strings.ReplaceAll(s, ">", "&gt;")
		s = strings.ReplaceAll(s, "\"", "&quot;")
		s = strings.ReplaceAll(s, "'", "&#039;")
		return NewString(s), nil
	}
	builtins["htmlspecialchars_decode"] = func(e *Env, a []Value) (Value, error) {
		s := a[0].ToString()
		s = strings.ReplaceAll(s, "&amp;", "&")
		s = strings.ReplaceAll(s, "&lt;", "<")
		s = strings.ReplaceAll(s, "&gt;", ">")
		s = strings.ReplaceAll(s, "&quot;", "\"")
		s = strings.ReplaceAll(s, "&#039;", "'")
		s = strings.ReplaceAll(s, "&#39;", "'")
		return NewString(s), nil
	}
}
