package phpgo

import "strings"

func init() {
	builtins["strtok"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		s := a[0].ToString()
		tokens := a[1].ToString()
		if s == "" {
			return NewBool(false), nil
		}
		idx := strings.IndexAny(s, tokens)
		if idx < 0 {
			return NewString(s), nil
		}
		return NewString(s[:idx]), nil
	}
}
