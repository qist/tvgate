package phpgo

import "sort"

func init() {
	builtins["ksort"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray {
			return NewBool(false), nil
		}
		ks := append([]string{}, a[0].Keys...)
		sort.Strings(ks)
		sorted := NewArray()
		for _, k := range ks {
			sorted.ArraySet(NewString(k), a[0].Arr[k])
		}
		writeRef(e, a[0], sorted)
		return NewBool(true), nil
	}
	builtins["asort"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray {
			return NewBool(false), nil
		}
		type kv struct {
			k string
			v Value
		}
		var kvs []kv
		for _, k := range a[0].Keys {
			kvs = append(kvs, kv{k, a[0].Arr[k]})
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
		if len(a) < 1 || a[0].Kind != KindArray {
			return NewBool(false), nil
		}
		vals := make([]string, 0, len(a[0].Keys))
		for _, k := range a[0].Keys {
			vals = append(vals, a[0].Arr[k].ToString())
		}
		sort.Strings(vals)
		sorted := NewArray()
		for _, s := range vals {
			sorted.ArraySet(NewInt(int64(len(sorted.Keys))), NewString(s))
		}
		writeRef(e, a[0], sorted)
		return NewBool(true), nil
	}
}
