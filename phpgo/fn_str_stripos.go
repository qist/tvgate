package phpgo

import "strings"

func init() {
	builtins["stripos"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		s := strings.ToLower(a[0].ToString())
		sub := strings.ToLower(a[1].ToString())
		offset := 0
		if len(a) >= 3 {
			offset = int(a[2].ToInt())
		}
		if offset < 0 {
			offset = len(s) + offset
			if offset < 0 {
				offset = 0
			}
		}
		if offset >= len(s) {
			return NewBool(false), nil
		}
		idx := strings.Index(s[offset:], sub)
		if idx < 0 {
			return NewBool(false), nil
		}
		return NewInt(int64(offset + idx)), nil
	}
}
