package phpgo

import "strings"

func init() {
	builtins["ucfirst"] = func(e *Env, a []Value) (Value, error) {
		s := a[0].ToString()
		if s == "" {
			return NewString(""), nil
		}
		return NewString(strings.ToUpper(s[:1]) + s[1:]), nil
	}
	builtins["lcfirst"] = func(e *Env, a []Value) (Value, error) {
		s := a[0].ToString()
		if s == "" {
			return NewString(""), nil
		}
		return NewString(strings.ToLower(s[:1]) + s[1:]), nil
	}
	builtins["ucwords"] = func(e *Env, a []Value) (Value, error) {
		return NewString(strings.Title(a[0].ToString())), nil
	}
}
