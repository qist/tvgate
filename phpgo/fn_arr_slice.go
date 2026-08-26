package phpgo

func init() {
	builtins["array_slice"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 || a[0].Kind != KindArray {
			return NewArray(), nil
		}
		offset := int(a[1].ToInt())
		length := -1
		if len(a) >= 3 {
			length = int(a[2].ToInt())
		}
		ks := a[0].Keys
		if offset < 0 {
			offset = len(ks) + offset
			if offset < 0 {
				offset = 0
			}
		}
		end := len(ks)
		if length >= 0 {
			end = offset + length
		} else if length < 0 {
			end = len(ks) + length
		}
		if end > len(ks) {
			end = len(ks)
		}
		if end < offset {
			end = offset
		}
		result := NewArray()
		idx := int64(0)
		for i := offset; i < end; i++ {
			result.ArraySet(NewInt(idx), a[0].Arr[ks[i]])
			idx++
		}
		return result, nil
	}
}
