package phpgo

// printf 系列：复用 phpSprintf 的格式化实现。
// printf/vprintf 直接输出并返回长度；sprintf/vsprintf 返回格式化字符串。
func init() {
	builtins["printf"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewInt(0), nil
		}
		s := phpSprintf(a[0].ToString(), valuesToIface(a[1:]))
		e.writeOutput(s)
		return NewInt(int64(len(s))), nil
	}
	builtins["vprintf"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewInt(0), nil
		}
		var args []Value
		if a[1].Kind == KindArray {
			for _, k := range a[1].Keys {
				args = append(args, a[1].Arr[k])
			}
		}
		s := phpSprintf(a[0].ToString(), valuesToIface(args))
		e.writeOutput(s)
		return NewInt(int64(len(s))), nil
	}
	builtins["vsprintf"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewString(""), nil
		}
		var args []Value
		if a[1].Kind == KindArray {
			for _, k := range a[1].Keys {
				args = append(args, a[1].Arr[k])
			}
		}
		return NewString(phpSprintf(a[0].ToString(), valuesToIface(args))), nil
	}
}
