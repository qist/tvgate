package phpgo

func init() {
	builtins["array_push"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray {
			return NewInt(0), nil
		}
		for i := 1; i < len(a); i++ {
			a[0].ArraySet(NewInt(int64(len(a[0].Keys))), a[i])
		}
		return NewInt(int64(len(a[0].Keys))), nil
	}
	builtins["array_pop"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray || len(a[0].Keys) == 0 {
			return NewNull(), nil
		}
		last := a[0].Keys[len(a[0].Keys)-1]
		v := a[0].Arr[last]
		delete(a[0].Arr, last)
		a[0].Keys = a[0].Keys[:len(a[0].Keys)-1]
		return v, nil
	}
	builtins["array_shift"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray || len(a[0].Keys) == 0 {
			return NewNull(), nil
		}
		first := a[0].Keys[0]
		v := a[0].Arr[first]
		delete(a[0].Arr, first)
		a[0].Keys = a[0].Keys[1:]
		return v, nil
	}
	builtins["array_unshift"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray {
			return NewInt(0), nil
		}
		// 构建 newKeys
		var newVals []Value
		for i := 1; i < len(a); i++ {
			newVals = append(newVals, a[i])
		}
		oldKVs := make([]Value, len(a[0].Keys))
		for i, k := range a[0].Keys {
			oldKVs[i] = a[0].Arr[k]
		}
		result := NewArray()
		for _, v := range newVals {
			result.ArraySet(NewInt(int64(len(result.Keys))), v)
		}
		for _, v := range oldKVs {
			result.ArraySet(NewInt(int64(len(result.Keys))), v)
		}
		writeRef(e, a[0], result)
		return NewInt(int64(len(result.Keys))), nil
	}
}
