package phpgo

func init() {
	builtins["array_rand"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray || len(a[0].Keys) == 0 {
			return NewNull(), nil
		}
		num := 1
		if len(a) >= 2 {
			num = int(a[1].ToInt())
		}
		keys := a[0].Keys
		if num == 1 {
			return NewString(keys[cryptoRandIntn(len(keys))]), nil
		}
		result := NewArray()
		indices := mathRandPerm(len(keys))
		for i := 0; i < num && i < len(keys); i++ {
			result.ArraySet(NewInt(int64(i)), NewString(keys[indices[i]]))
		}
		return result, nil
	}
}
