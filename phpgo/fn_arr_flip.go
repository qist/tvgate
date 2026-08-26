package phpgo

func init() {
	builtins["array_flip"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray {
			return NewArray(), nil
		}
		result := NewArray()
		for _, k := range a[0].Keys {
			result.ArraySet(a[0].Arr[k], NewString(k))
		}
		return result, nil
	}
}
