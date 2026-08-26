package phpgo

func init() {
	builtins["str_split"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewArray(), nil
		}
		s := a[0].ToString()
		chunk := 1
		if len(a) >= 2 {
			chunk = int(a[1].ToInt())
		}
		if chunk < 1 {
			chunk = 1
		}
		arr := NewArray()
		for i := 0; i < len(s); i += chunk {
			end := i + chunk
			if end > len(s) {
				end = len(s)
			}
			arr.ArraySet(NewInt(int64(len(arr.Keys))), NewString(s[i:end]))
		}
		return arr, nil
	}
}
