package phpgo

import "strings"

func init() {
	builtins["explode"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewArray(), nil
		}
		delim := a[0].ToString()
		s := a[1].ToString()
		if delim == "" {
			return NewBool(false), nil
		}
		parts := strings.Split(s, delim)
		arr := NewArray()
		for i, p := range parts {
			arr.ArraySet(NewInt(int64(i)), NewString(p))
		}
		return arr, nil
	}
}
