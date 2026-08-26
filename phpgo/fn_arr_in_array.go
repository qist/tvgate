package phpgo

func init() {
	builtins["in_array"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 || a[1].Kind != KindArray {
			return NewBool(false), nil
		}
		needle := a[0].ToString()
		strict := false
		if len(a) >= 3 {
			strict = a[2].ToBool()
		}
		for _, k := range a[1].Keys {
			v := a[1].Arr[k]
			if strict {
				if a[0].Kind == v.Kind && a[0].ToString() == v.ToString() {
					return NewBool(true), nil
				}
			} else {
				if v.ToString() == needle {
					return NewBool(true), nil
				}
			}
		}
		return NewBool(false), nil
	}
}
