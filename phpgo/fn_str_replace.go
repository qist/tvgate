package phpgo

import "strings"

func init() {
	builtins["str_replace"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 3 {
			return NewString(""), nil
		}
		return NewString(strings.ReplaceAll(a[2].ToString(), a[0].ToString(), a[1].ToString())), nil
	}
}
