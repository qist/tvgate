package phpgo

import "strings"

func init() {
	builtins["str_pad"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewString(""), nil
		}
		s := a[0].ToString()
		length := int(a[1].ToInt())
		padStr := " "
		if len(a) >= 3 {
			padStr = a[2].ToString()
		}
		padType := 1 // STR_PAD_RIGHT
		if len(a) >= 4 {
			padType = int(a[3].ToInt())
		}
		if length <= len(s) || padStr == "" {
			return NewString(s), nil
		}
		padLen := length - len(s)
		fullPad := strings.Repeat(padStr, (padLen/len(padStr))+1)
		switch padType {
		case 0: // STR_PAD_LEFT
			return NewString(fullPad[:padLen] + s), nil
		case 2: // STR_PAD_BOTH
			left := padLen / 2
			right := padLen - left
			return NewString(fullPad[:left] + s + fullPad[:right]), nil
		default: // STR_PAD_RIGHT
			return NewString(s + fullPad[:padLen]), nil
		}
	}
}
