package phpgo

// 数组集合运算：diff / intersect（按值或按 key）
func init() {
	// array_diff：返回 $array 中不在任何后续数组中出现的值（字符串比较，保留 $array 的键）
	builtins["array_diff"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray {
			return NewArray(), nil
		}
		exclude := map[string]bool{}
		for _, other := range a[1:] {
			if other.Kind != KindArray {
				continue
			}
			for _, k := range other.Keys {
				exclude[other.Arr[k].ToString()] = true
			}
		}
		result := NewArray()
		for _, k := range a[0].Keys {
			v := a[0].Arr[k]
			if !exclude[v.ToString()] {
				result.ArraySet(NewString(k), v)
			}
		}
		return result, nil
	}
	// array_intersect：返回 $array 中同时出现在所有后续数组中的值（保留 $array 的键）
	builtins["array_intersect"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray {
			return NewArray(), nil
		}
		result := NewArray()
		for _, k := range a[0].Keys {
			v := a[0].Arr[k]
			ok := true
			for _, other := range a[1:] {
				if other.Kind != KindArray {
					ok = false
					break
				}
				found := false
				for _, kk := range other.Keys {
					if other.Arr[kk].ToString() == v.ToString() {
						found = true
						break
					}
				}
				if !found {
					ok = false
					break
				}
			}
			if ok {
				result.ArraySet(NewString(k), v)
			}
		}
		return result, nil
	}
	// array_diff_key：按键比较，返回 $array 中键不在后续数组中的元素
	builtins["array_diff_key"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray {
			return NewArray(), nil
		}
		exclude := map[string]bool{}
		for _, other := range a[1:] {
			if other.Kind != KindArray {
				continue
			}
			for _, kk := range other.Keys {
				exclude[kk] = true
			}
		}
		result := NewArray()
		for _, k := range a[0].Keys {
			if !exclude[k] {
				result.ArraySet(NewString(k), a[0].Arr[k])
			}
		}
		return result, nil
	}
	// array_intersect_key：按键比较，返回 $array 中键同时存在于所有后续数组中的元素
	builtins["array_intersect_key"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray {
			return NewArray(), nil
		}
		result := NewArray()
		for _, k := range a[0].Keys {
			ok := true
			for _, other := range a[1:] {
				if other.Kind != KindArray || !other.IsArrayKeyExists(NewString(k)) {
					ok = false
					break
				}
			}
			if ok {
				result.ArraySet(NewString(k), a[0].Arr[k])
			}
		}
		return result, nil
	}
	// array_merge_recursive：递归合并；字符串键同名时若双方都是数组则递归合并为数组
	builtins["array_merge_recursive"] = func(e *Env, a []Value) (Value, error) {
		result := NewArray()
		for _, arr := range a {
			if arr.Kind != KindArray {
				continue
			}
			for _, k := range arr.Keys {
				v := arr.Arr[k]
				existing, has := result.Arr[k]
				if !has {
					result.ArraySet(NewString(k), v)
					continue
				}
				if isNumericKey(k) {
					// 数字键：追加为新元素
					result.ArraySet(NewInt(int64(len(result.Keys))), v)
					continue
				}
				// 同名字符串键：双方都是数组则递归合并，否则包成数组
				if existing.Kind == KindArray && v.Kind == KindArray {
					merged, _ := builtins["array_merge_recursive"](e, []Value{existing, v})
					result.ArraySet(NewString(k), merged)
				} else {
					combined := NewArray()
					combined.ArraySet(NewInt(0), existing)
					combined.ArraySet(NewInt(1), v)
					result.ArraySet(NewString(k), combined)
				}
			}
		}
		return result, nil
	}
}
