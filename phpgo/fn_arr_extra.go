package phpgo

// 数组工具函数：chunk / splice / range / shuffle / fill_keys / pad / count_values / product / reduce
func init() {
	// array_chunk：按 size 分块；preserve_keys=true 时保留原键
	builtins["array_chunk"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 || a[0].Kind != KindArray {
			return NewArray(), nil
		}
		size := int(a[1].ToInt())
		if size < 1 {
			return NewArray(), nil
		}
		preserve := false
		if len(a) >= 3 {
			preserve = a[2].ToBool()
		}
		var chunks []Value
		for _, k := range a[0].Keys {
			if len(chunks) == 0 || len(chunks[len(chunks)-1].Keys) >= size {
				chunks = append(chunks, NewArray())
			}
			cur := &chunks[len(chunks)-1]
			if preserve {
				cur.ArraySet(NewString(k), a[0].Arr[k])
			} else {
				cur.ArraySet(NewInt(int64(len(cur.Keys))), a[0].Arr[k])
			}
		}
		result := NewArray()
		for _, c := range chunks {
			result.ArraySet(NewInt(int64(len(result.Keys))), c)
		}
		return result, nil
	}
	// array_splice：删除并替换一段元素，返回被删除部分（原数组按引用修改，数字键重新索引）
	builtins["array_splice"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewArray(), nil
		}
		arr := deref(a[0])
		if arr.Kind != KindArray {
			return NewArray(), nil
		}
		vals := make([]Value, 0, len(arr.Keys))
		for _, k := range arr.Keys {
			vals = append(vals, arr.Arr[k])
		}
		offset := a[1].ToInt()
		if offset < 0 {
			offset = int64(len(vals)) + offset
		}
		if offset < 0 {
			offset = 0
		}
		if offset > int64(len(vals)) {
			offset = int64(len(vals))
		}
		length := int64(len(vals)) - offset
		if len(a) >= 3 {
			length = a[2].ToInt()
			if length < 0 {
				length = int64(len(vals)) - offset + length
			}
		}
		if length < 0 {
			length = 0
		}
		if offset+length > int64(len(vals)) {
			length = int64(len(vals)) - offset
		}
		// 替换值
		var repl []Value
		if len(a) >= 4 {
			rv := a[3]
			if rv.Kind == KindArray {
				for _, k := range rv.Keys {
					repl = append(repl, rv.Arr[k])
				}
			} else {
				repl = append(repl, rv)
			}
		}
		removed := append([]Value{}, vals[offset:offset+length]...)
		newVals := make([]Value, 0, len(vals)-len(removed)+len(repl))
		newVals = append(newVals, vals[:offset]...)
		newVals = append(newVals, repl...)
		newVals = append(newVals, vals[offset+length:]...)
		newArr := NewArray()
		for _, v := range newVals {
			newArr.ArraySet(NewInt(int64(len(newArr.Keys))), v)
		}
		writeRef(e, a[0], newArr)
		remArr := NewArray()
		for _, v := range removed {
			remArr.ArraySet(NewInt(int64(len(remArr.Keys))), v)
		}
		return remArr, nil
	}
	// range：生成低到高的等差序列（支持整数/字符）
	builtins["range"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewArray(), nil
		}
		step := int64(1)
		if len(a) >= 3 && a[2].ToInt() != 0 {
			step = a[2].ToInt()
		}
		// 字符范围
		if a[0].Kind == KindString && a[1].Kind == KindString && len(a[0].Str) == 1 && len(a[1].Str) == 1 {
			lo := int64(a[0].Str[0])
			hi := int64(a[1].Str[0])
			if step < 0 {
				lo, hi = hi, lo
			}
			result := NewArray()
			if lo <= hi {
				for i := lo; i <= hi; i += step {
					result.ArraySet(NewInt(int64(len(result.Keys))), NewString(string([]byte{byte(i)})))
				}
			}
			return result, nil
		}
		lo := a[0].ToInt()
		hi := a[1].ToInt()
		result := NewArray()
		if step > 0 {
			for i := lo; i <= hi; i += step {
				result.ArraySet(NewInt(int64(len(result.Keys))), NewInt(i))
			}
		} else {
			for i := lo; i >= hi; i += step {
				result.ArraySet(NewInt(int64(len(result.Keys))), NewInt(i))
			}
		}
		return result, nil
	}
	// shuffle：随机打乱数组（按引用修改，重新索引）
	builtins["shuffle"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		arr := deref(a[0])
		if arr.Kind != KindArray {
			return NewBool(false), nil
		}
		vals := make([]Value, 0, len(arr.Keys))
		for _, k := range arr.Keys {
			vals = append(vals, arr.Arr[k])
		}
		perm := mathRandPerm(len(vals))
		newArr := NewArray()
		for _, idx := range perm {
			newArr.ArraySet(NewInt(int64(len(newArr.Keys))), vals[idx])
		}
		writeRef(e, a[0], newArr)
		return NewBool(true), nil
	}
	// array_fill_keys：用指定 keys 填充统一值
	builtins["array_fill_keys"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewArray(), nil
		}
		val := a[1]
		result := NewArray()
		if a[0].Kind != KindArray {
			return result, nil
		}
		for _, k := range a[0].Keys {
			result.ArraySet(NewString(a[0].Arr[k].ToString()), val)
		}
		return result, nil
	}
	// array_pad：把数组填充到指定长度（size 为正右补、负左补）
	builtins["array_pad"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 3 {
			return NewArray(), nil
		}
		arr := a[0]
		if arr.Kind != KindArray {
			arr = NewArray()
		}
		size := a[1].ToInt()
		val := a[2]
		result := NewArray()
		if size < 0 {
			size = -size
		}
		cur := int64(len(arr.Keys))
		if cur >= size {
			// 已够长，直接复制
			for _, k := range arr.Keys {
				result.ArraySet(NewString(k), arr.Arr[k])
			}
			return result, nil
		}
		pad := size - cur
		if a[1].ToInt() < 0 {
			// 左补
			for i := int64(0); i < pad; i++ {
				result.ArraySet(NewInt(int64(len(result.Keys))), val)
			}
		}
		for _, k := range arr.Keys {
			result.ArraySet(NewInt(int64(len(result.Keys))), arr.Arr[k])
		}
		if a[1].ToInt() > 0 {
			for i := int64(0); i < pad; i++ {
				result.ArraySet(NewInt(int64(len(result.Keys))), val)
			}
		}
		return result, nil
	}
	// array_count_values：统计各值的出现次数
	builtins["array_count_values"] = func(e *Env, a []Value) (Value, error) {
		result := NewArray()
		if len(a) < 1 || a[0].Kind != KindArray {
			return result, nil
		}
		counts := map[string]int64{}
		for _, k := range a[0].Keys {
			counts[a[0].Arr[k].ToString()]++
		}
		// 按首次出现顺序输出
		seen := map[string]bool{}
		for _, k := range a[0].Keys {
			valStr := a[0].Arr[k].ToString()
			if seen[valStr] {
				continue
			}
			seen[valStr] = true
			result.ArraySet(NewString(valStr), NewInt(counts[valStr]))
		}
		return result, nil
	}
	// array_product：所有元素乘积
	builtins["array_product"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray {
			return NewInt(0), nil
		}
		prod := int64(1)
		if len(a[0].Keys) == 0 {
			return NewInt(1), nil
		}
		for _, k := range a[0].Keys {
			prod *= a[0].Arr[k].ToInt()
		}
		return NewInt(prod), nil
	}
	// array_reduce：用回调把数组归约为单值
	builtins["array_reduce"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 || a[0].Kind != KindArray {
			return NewNull(), nil
		}
		callback := a[1].ToString()
		carry := NewNull()
		if len(a) >= 3 {
			carry = a[2]
		}
		for _, k := range a[0].Keys {
			item := a[0].Arr[k]
			var r Value
			if bf, ok := builtins[callback]; ok {
				r, _ = bf(e, []Value{carry, item})
			} else if fn, ok := e.funcs[callback]; ok {
				r, _ = e.callUserFuncValues(fn, []Value{carry, item})
			} else {
				return NewNull(), nil
			}
			carry = r
		}
		return carry, nil
	}
	// array_key_first / array_key_last：取首/尾键（PHP 7.3+）
	builtins["array_key_first"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray || len(a[0].Keys) == 0 {
			return NewNull(), nil
		}
		return NewString(a[0].Keys[0]), nil
	}
	builtins["array_key_last"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray || len(a[0].Keys) == 0 {
			return NewNull(), nil
		}
		return NewString(a[0].Keys[len(a[0].Keys)-1]), nil
	}
}
