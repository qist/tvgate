package phpgo

func init() {
	builtins["array_filter"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray {
			return NewArray(), nil
		}
		fnName := ""
		if len(a) >= 2 {
			fnName = a[1].ToString()
		}
		result := NewArray()
		for _, k := range a[0].Keys {
			v := a[0].Arr[k]
			keep := true
			if fnName != "" {
				if bf, ok := builtins[fnName]; ok {
					r, _ := bf(e, []Value{v})
					keep = r.ToBool()
				} else if fn, ok := e.funcs[fnName]; ok {
					r, _ := e.callUserFuncValues(fn, []Value{v})
					keep = r.ToBool()
				}
			} else {
				keep = v.ToBool()
			}
			if keep {
				result.ArraySet(NewString(k), v)
			}
		}
		return result, nil
	}
}
