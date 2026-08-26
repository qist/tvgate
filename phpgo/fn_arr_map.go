package phpgo

func init() {
	builtins["array_map"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewArray(), nil
		}
		fnName := a[0].ToString()
		arr := a[1]
		if arr.Kind != KindArray {
			return NewArray(), nil
		}
		result := NewArray()
		for _, k := range arr.Keys {
			v := arr.Arr[k]
			var mapped Value
			if bf, ok := builtins[fnName]; ok {
				mapped, _ = bf(e, []Value{v})
			} else if fn, ok := e.funcs[fnName]; ok {
				mapped, _ = e.callUserFuncValues(fn, []Value{v})
			} else {
				mapped = v
			}
			result.ArraySet(NewString(k), mapped)
		}
		return result, nil
	}
}
