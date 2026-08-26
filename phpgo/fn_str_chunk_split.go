package phpgo

import "strings"

func init() {
	builtins["chunk_split"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewString(""), nil
		}
		s := a[0].ToString()
		chunkLen := 76
		end := "\r\n"
		if len(a) >= 2 {
			chunkLen = int(a[1].ToInt())
		}
		if len(a) >= 3 {
			end = a[2].ToString()
		}
		if chunkLen < 1 {
			chunkLen = 1
		}
		var b strings.Builder
		for i := 0; i < len(s); i += chunkLen {
			e2 := i + chunkLen
			if e2 > len(s) {
				e2 = len(s)
			}
			b.WriteString(s[i:e2])
			b.WriteString(end)
		}
		return NewString(b.String()), nil
	}
}
