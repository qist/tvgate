package phpgo

import "strings"

func init() {
	builtins["rtrim"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewString(""), nil
		}
		chars := " \t\n\r\x00\x0B"
		if len(a) >= 2 {
			chars = a[1].ToString()
		}
		return NewString(strings.TrimRight(a[0].ToString(), chars)), nil
	}
}
