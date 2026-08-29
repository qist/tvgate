package phpgo

import "sort"

// 数组排序增强：arsort/krsort（内置比较）与 usort/uasort/uksort（用户回调比较）
func init() {
	builtins["arsort"] = func(e *Env, a []Value) (Value, error) {
		arr := deref(a[0])
		if len(a) < 1 || arr.Kind != KindArray {
			return NewBool(false), nil
		}
		type kv struct {
			k string
			v Value
		}
		var kvs []kv
		for _, k := range arr.Keys {
			kvs = append(kvs, kv{k, arr.Arr[k]})
		}
		sort.Slice(kvs, func(i, j int) bool {
			return kvs[i].v.ToString() > kvs[j].v.ToString()
		})
		sorted := NewArray()
		for _, p := range kvs {
			sorted.ArraySet(NewString(p.k), p.v)
		}
		writeRef(e, a[0], sorted)
		return NewBool(true), nil
	}
	builtins["krsort"] = func(e *Env, a []Value) (Value, error) {
		arr := deref(a[0])
		if len(a) < 1 || arr.Kind != KindArray {
			return NewBool(false), nil
		}
		ks := append([]string{}, arr.Keys...)
		sort.Slice(ks, func(i, j int) bool { return ks[i] > ks[j] })
		sorted := NewArray()
		for _, k := range ks {
			sorted.ArraySet(NewString(k), arr.Arr[k])
		}
		writeRef(e, a[0], sorted)
		return NewBool(true), nil
	}
	// usort：用户回调比较，重新索引为数字键
	builtins["usort"] = func(e *Env, a []Value) (Value, error) {
		arr := deref(a[0])
		if len(a) < 2 || arr.Kind != KindArray {
			return NewBool(false), nil
		}
		vals := make([]Value, 0, len(arr.Keys))
		for _, k := range arr.Keys {
			vals = append(vals, arr.Arr[k])
		}
		cb := a[1].ToString()
		stableSortValues(e, vals, cb)
		sorted := NewArray()
		for _, v := range vals {
			sorted.ArraySet(NewInt(int64(len(sorted.Keys))), v)
		}
		writeRef(e, a[0], sorted)
		return NewBool(true), nil
	}
	// uasort：用户回调比较，保留键
	builtins["uasort"] = func(e *Env, a []Value) (Value, error) {
		arr := deref(a[0])
		if len(a) < 2 || arr.Kind != KindArray {
			return NewBool(false), nil
		}
		type kv struct {
			k string
			v Value
		}
		var kvs []kv
		for _, k := range arr.Keys {
			kvs = append(kvs, kv{k, arr.Arr[k]})
		}
		cb := a[1].ToString()
		sort.Slice(kvs, func(i, j int) bool {
			r, _ := callCallable(e, NewString(cb), []Value{kvs[i].v, kvs[j].v})
			return r.ToInt() < 0
		})
		sorted := NewArray()
		for _, p := range kvs {
			sorted.ArraySet(NewString(p.k), p.v)
		}
		writeRef(e, a[0], sorted)
		return NewBool(true), nil
	}
	// uksort：用户回调比较键，保留键
	builtins["uksort"] = func(e *Env, a []Value) (Value, error) {
		arr := deref(a[0])
		if len(a) < 2 || arr.Kind != KindArray {
			return NewBool(false), nil
		}
		ks := append([]string{}, arr.Keys...)
		cb := a[1].ToString()
		sort.Slice(ks, func(i, j int) bool {
			r, _ := callCallable(e, NewString(cb), []Value{NewString(ks[i]), NewString(ks[j])})
			return r.ToInt() < 0
		})
		sorted := NewArray()
		for _, k := range ks {
			sorted.ArraySet(NewString(k), arr.Arr[k])
		}
		writeRef(e, a[0], sorted)
		return NewBool(true), nil
	}
}

// stableSortValues 用用户回调对值排序（稳定排序，重排后用于 usort）
func stableSortValues(e *Env, vals []Value, cb string) {
	idx := make([]int, len(vals))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(i, j int) bool {
		r, _ := callCallable(e, NewString(cb), []Value{vals[idx[i]], vals[idx[j]]})
		return r.ToInt() < 0
	})
	ordered := make([]Value, len(vals))
	for i, id := range idx {
		ordered[i] = vals[id]
	}
	copy(vals, ordered)
}
