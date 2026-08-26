package phpgo

import "strings"

func init() {
	builtins["str_contains"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		return NewBool(strings.Contains(a[0].ToString(), a[1].ToString())), nil
	}
	builtins["str_starts_with"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		return NewBool(strings.HasPrefix(a[0].ToString(), a[1].ToString())), nil
	}
	builtins["str_ends_with"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		return NewBool(strings.HasSuffix(a[0].ToString(), a[1].ToString())), nil
	}
}
