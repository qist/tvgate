package phpgo

import "strconv"

func init() {
	builtins["strval"] = func(e *Env, a []Value) (Value, error) {
		return NewString(a[0].ToString()), nil
	}
	builtins["floatval"] = func(e *Env, a []Value) (Value, error) {
		f, _ := strconv.ParseFloat(a[0].ToString(), 64)
		return NewFloat(f), nil
	}
	builtins["doubleval"] = builtins["floatval"]
	builtins["intval"] = func(e *Env, a []Value) (Value, error) {
		base := 10
		if len(a) >= 2 {
			base = int(a[1].ToInt())
		}
		s := a[0].ToString()
		switch base {
		case 16:
			n, _ := strconv.ParseInt(s, 16, 64)
			return NewInt(n), nil
		case 8:
			n, _ := strconv.ParseInt(s, 8, 64)
			return NewInt(n), nil
		case 2:
			n, _ := strconv.ParseInt(s, 2, 64)
			return NewInt(n), nil
		default:
			return NewInt(a[0].ToInt()), nil
		}
	}
}
