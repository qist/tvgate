package phpgo

import "strings"

func init() {
	builtins["strrpos"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		idx := strings.LastIndex(a[0].ToString(), a[1].ToString())
		if idx < 0 {
			return NewBool(false), nil
		}
		return NewInt(int64(idx)), nil
	}
}
