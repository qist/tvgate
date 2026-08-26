package phpgo

import "strings"

func init() {
	builtins["implode"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewString(""), nil
		}
		var glue string
		var arr Value
		if a[0].Kind == KindArray {
			arr = a[0]
			glue = ""
			if len(a) >= 2 {
				glue = a[1].ToString()
			}
		} else {
			glue = a[0].ToString()
			if len(a) < 2 || a[1].Kind != KindArray {
				return NewString(""), nil
			}
			arr = a[1]
		}
		var parts []string
		for _, k := range arr.Keys {
			parts = append(parts, arr.Arr[k].ToString())
		}
		return NewString(strings.Join(parts, glue)), nil
	}
	builtins["join"] = builtins["implode"] // alias
}
