package phpgo

import "strings"

func init() {
	builtins["nl2br"] = func(e *Env, a []Value) (Value, error) {
		s := a[0].ToString()
		s = strings.ReplaceAll(s, "\r\n", "<br />\r\n")
		s = strings.ReplaceAll(s, "\n", "<br />\n")
		s = strings.ReplaceAll(s, "\r", "<br />\r")
		return NewString(s), nil
	}
	builtins["addslashes"] = func(e *Env, a []Value) (Value, error) {
		s := a[0].ToString()
		s = strings.ReplaceAll(s, "\\", "\\\\")
		s = strings.ReplaceAll(s, "'", "\\'")
		s = strings.ReplaceAll(s, "\"", "\\\"")
		return NewString(s), nil
	}
	builtins["stripslashes"] = func(e *Env, a []Value) (Value, error) {
		s := a[0].ToString()
		s = strings.ReplaceAll(s, "\\\\", "\\")
		s = strings.ReplaceAll(s, "\\'", "'")
		s = strings.ReplaceAll(s, "\\\"", "\"")
		return NewString(s), nil
	}
	builtins["wordwrap"] = func(e *Env, a []Value) (Value, error) {
		s := a[0].ToString()
		width := 75
		brk := "\n"
		if len(a) >= 2 {
			width = int(a[1].ToInt())
		}
		if len(a) >= 3 {
			brk = a[2].ToString()
		}
		if width < 1 {
			width = 1
		}
		var b strings.Builder
		for _, line := range strings.Split(s, "\n") {
			for len(line) > width {
				b.WriteString(line[:width])
				b.WriteString(brk)
				line = line[width:]
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
		result := b.String()
		if len(result) > 0 {
			result = strings.TrimSuffix(result, "\n")
		}
		return NewString(result), nil
	}
}
