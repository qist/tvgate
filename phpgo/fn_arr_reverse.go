package phpgo

func init() {
	builtins["array_reverse"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray {
			return NewArray(), nil
		}
		result := NewArray()
		ks := append([]string{}, a[0].Keys...)
		for i := len(ks) - 1; i >= 0; i-- {
			k := ks[i]
			if isNumericKey(k) {
				result.ArraySet(NewInt(int64(len(result.Keys))), a[0].Arr[k])
			} else {
				result.ArraySet(NewString(k), a[0].Arr[k])
			}
		}
		return result, nil
	}
}
