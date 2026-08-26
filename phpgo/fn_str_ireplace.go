package phpgo

import "strings"

func init() {
	builtins["str_ireplace"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 3 {
			return NewString(""), nil
		}
		search := strings.ToLower(a[0].ToString())
		replace := a[1].ToString()
		subjOrig := a[2].ToString()
		subjLower := strings.ToLower(subjOrig)
		var b strings.Builder
		idx := 0
		for {
			pos := strings.Index(subjLower[idx:], search)
			if pos < 0 {
				b.WriteString(subjOrig[idx:])
				break
			}
			b.WriteString(subjOrig[idx : idx+pos])
			b.WriteString(replace)
			idx += pos + len(search)
		}
		return NewString(b.String()), nil
	}
}
