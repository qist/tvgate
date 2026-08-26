package phpgo

func init() {
	builtins["sprintf"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewString(""), nil
		}
		format := a[0].ToString()
		args := make([]interface{}, 0, len(a)-1)
		for _, v := range a[1:] {
			switch v.Kind {
			case KindInt:
				args = append(args, v.Int)
			case KindFloat:
				args = append(args, v.Float)
			case KindString:
				args = append(args, v.Str)
			case KindBool:
				args = append(args, v.Bool)
			default:
				args = append(args, v.ToString())
			}
		}
		return NewString(phpSprintf(format, args)), nil
	}
}
