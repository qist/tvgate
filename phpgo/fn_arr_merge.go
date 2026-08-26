package phpgo

func init() {
	builtins["array_merge"] = func(e *Env, a []Value) (Value, error) {
		result := NewArray()
		var nextIdx int64 = 0
		for _, arr := range a {
			if arr.Kind != KindArray {
				continue
			}
			for _, k := range arr.Keys {
				v := arr.Arr[k]
				if isNumericKey(k) {
					result.ArraySet(NewInt(nextIdx), v)
					nextIdx++
				} else {
					result.ArraySet(NewString(k), v)
				}
			}
		}
		return result, nil
	}
}
