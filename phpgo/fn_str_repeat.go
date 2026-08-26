package phpgo

import "strings"

func init() {
	builtins["str_repeat"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewString(""), nil
		}
		n := int(a[1].ToInt())
		if n < 0 {
			n = 0
		}
		return NewString(strings.Repeat(a[0].ToString(), n)), nil
	}
}
