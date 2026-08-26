package phpgo

func init() {
	builtins["array_fill"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 3 {
			return NewArray(), nil
		}
		start := a[0].ToInt()
		count := int(a[1].ToInt())
		result := NewArray()
		for i := 0; i < count; i++ {
			result.ArraySet(NewInt(start+int64(i)), a[2])
		}
		return result, nil
	}
}
