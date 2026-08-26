package phpgo

func init() {
	builtins["end"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray || len(a[0].Keys) == 0 {
			return NewBool(false), nil
		}
		return a[0].Arr[a[0].Keys[len(a[0].Keys)-1]], nil
	}
	builtins["current"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray || len(a[0].Keys) == 0 {
			return NewBool(false), nil
		}
		return a[0].Arr[a[0].Keys[0]], nil
	}
	builtins["next"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray || len(a[0].Keys) < 2 {
			return NewBool(false), nil
		}
		return a[0].Arr[a[0].Keys[1]], nil
	}
	builtins["prev"] = func(e *Env, a []Value) (Value, error) {
		return NewBool(false), nil
	}
	builtins["reset"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray || len(a[0].Keys) == 0 {
			return NewBool(false), nil
		}
		return a[0].Arr[a[0].Keys[0]], nil
	}
}
