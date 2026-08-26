package phpgo

func init() {
	builtins["array_column"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 || a[0].Kind != KindArray {
			return NewArray(), nil
		}
		colKey := a[1].ToString()
		result := NewArray()
		for _, k := range a[0].Keys {
			row := a[0].Arr[k]
			if row.Kind != KindArray {
				continue
			}
			if v, ok := row.Arr[colKey]; ok {
				result.ArraySet(NewInt(int64(len(result.Keys))), v)
			}
		}
		return result, nil
	}
	builtins["array_combine"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 || a[0].Kind != KindArray || a[1].Kind != KindArray {
			return NewArray(), nil
		}
		result := NewArray()
		for i, k := range a[0].Keys {
			if i < len(a[1].Keys) {
				result.ArraySet(a[0].Arr[k], a[1].Arr[a[1].Keys[i]])
			}
		}
		return result, nil
	}
	builtins["array_unique"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray {
			return NewArray(), nil
		}
		seen := map[string]bool{}
		result := NewArray()
		for _, k := range a[0].Keys {
			v := a[0].Arr[k].ToString()
			if !seen[v] {
				seen[v] = true
				result.ArraySet(NewString(k), a[0].Arr[k])
			}
		}
		return result, nil
	}
	builtins["array_search"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 || a[1].Kind != KindArray {
			return NewBool(false), nil
		}
		needle := a[0].ToString()
		for _, k := range a[1].Keys {
			if a[1].Arr[k].ToString() == needle {
				return NewString(k), nil
			}
		}
		return NewBool(false), nil
	}
	builtins["array_sum"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray {
			return NewInt(0), nil
		}
		var sum int64
		for _, k := range a[0].Keys {
			sum += a[0].Arr[k].ToInt()
		}
		return NewInt(sum), nil
	}
	builtins["compact"] = func(e *Env, a []Value) (Value, error) {
		result := NewArray()
		for _, v := range a {
			name := v.ToString()
			if val, ok := e.vars[name]; ok {
				result.ArraySet(NewString(name), val)
			}
		}
		return result, nil
	}
}
