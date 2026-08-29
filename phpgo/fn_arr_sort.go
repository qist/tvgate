package phpgo

import "sort"

func init() {
	builtins["ksort"] = func(e *Env, a []Value) (Value, error) {
		arr := deref(a[0])
		if len(a) < 1 || arr.Kind != KindArray {
			return NewBool(false), nil
		}
		ks := append([]string{}, arr.Keys...)
		sort.Strings(ks)
		sorted := NewArray()
		for _, k := range ks {
			sorted.ArraySet(NewString(k), arr.Arr[k])
		}
		writeRef(e, a[0], sorted)
		return NewBool(true), nil
	}
	builtins["asort"] = func(e *Env, a []Value) (Value, error) {
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
			return kvs[i].v.ToString() < kvs[j].v.ToString()
		})
		sorted := NewArray()
		for _, p := range kvs {
			sorted.ArraySet(NewString(p.k), p.v)
		}
		writeRef(e, a[0], sorted)
		return NewBool(true), nil
	}
	builtins["sort"] = func(e *Env, a []Value) (Value, error) {
		arr := deref(a[0])
		if len(a) < 1 || arr.Kind != KindArray {
			return NewBool(false), nil
		}
		type kv struct {
			v Value
		}
		var items []kv
		for _, k := range arr.Keys {
			items = append(items, kv{arr.Arr[k]})
		}
		sort.Slice(items, func(i, j int) bool {
			return items[i].v.ToString() < items[j].v.ToString()
		})
		sorted := NewArray()
		for _, it := range items {
			sorted.ArraySet(NewInt(int64(len(sorted.Keys))), it.v)
		}
		writeRef(e, a[0], sorted)
		return NewBool(true), nil
	}
}
