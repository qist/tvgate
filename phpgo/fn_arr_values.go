package phpgo

func init() {
	builtins["array_values"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray {
			return NewArray(), nil
		}
		result := NewArray()
		for _, k := range a[0].Keys {
			result.ArraySet(NewInt(int64(len(result.Keys))), a[0].Arr[k])
		}
		return result, nil
	}
}
