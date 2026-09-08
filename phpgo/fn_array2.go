package phpgo

// 数组函数补齐（第二批）：
// array_replace / array_replace_recursive / array_change_key_case / array_intersect_assoc /
// array_diff_assoc / array_uintersect / array_udiff / array_intersect_ukey / array_is_list /
// array_walk / array_walk_recursive / array_multisort。

import (
	"sort"
	"strconv"
	"strings"
)

// phpArrayReplace 实现 array_replace / array_replace_recursive。
func phpArrayReplace(e *Env, arrays []Value, recursive bool) Value {
	out := NewArray()
	for _, av := range arrays {
		arr := deref(av)
		if arr.Kind != KindArray {
			continue
		}
		for _, k := range arr.Keys {
			v := arr.Arr[k]
			cur, has := out.Arr[k]
			if recursive && has && !isNumericKey(k) && cur.Kind == KindArray && v.Kind == KindArray {
				merged := phpArrayReplace(e, []Value{cur, v}, true)
				out.ArraySet(NewString(k), merged)
				continue
			}
			out.ArraySet(NewString(k), v)
		}
	}
	return out
}

// phpValStr 数组值相等比较（沿用 array_intersect/array_diff 的字符串比较约定）
func phpValStr(v Value) string { return deref(v).ToString() }

func init() {
	builtins["array_replace"] = func(e *Env, a []Value) (Value, error) {
		return phpArrayReplace(e, a, false), nil
	}
	builtins["array_replace_recursive"] = func(e *Env, a []Value) (Value, error) {
		return phpArrayReplace(e, a, true), nil
	}
	// array_change_key_case($array, $case = CASE_LOWER)：仅转换字符串键
	builtins["array_change_key_case"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray {
			return NewArray(), nil
		}
		upper := false
		if len(a) >= 2 {
			upper = a[1].ToInt() == 1
		}
		src := deref(a[0])
		out := NewArray()
		for _, k := range src.Keys {
			kk := k
			if !isNumericKey(k) {
				if upper {
					kk = strings.ToUpper(k)
				} else {
					kk = strings.ToLower(k)
				}
			}
			out.ArraySet(NewString(kk), src.Arr[k])
		}
		return out, nil
	}
	// array_intersect_assoc：值与键同时匹配（字符串比较）
	builtins["array_intersect_assoc"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray {
			return NewArray(), nil
		}
		first := deref(a[0])
		out := NewArray()
		for _, k := range first.Keys {
			v := first.Arr[k]
			ok := true
			for _, other := range a[1:] {
				o := deref(other)
				if o.Kind != KindArray {
					ok = false
					break
				}
				ov, has := o.Arr[k]
				if !has || ov.ToString() != v.ToString() {
					ok = false
					break
				}
			}
			if ok {
				out.ArraySet(NewString(k), v)
			}
		}
		return out, nil
	}
	// array_diff_assoc：值与键任一不同即保留
	builtins["array_diff_assoc"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray {
			return NewArray(), nil
		}
		first := deref(a[0])
		out := NewArray()
		for _, k := range first.Keys {
			v := first.Arr[k]
			excluded := false
			for _, other := range a[1:] {
				o := deref(other)
				if o.Kind != KindArray {
					continue
				}
				if ov, has := o.Arr[k]; has && ov.ToString() == v.ToString() {
					excluded = true
					break
				}
			}
			if !excluded {
				out.ArraySet(NewString(k), v)
			}
		}
		return out, nil
	}
	// array_uintersect / array_udiff / array_intersect_ukey：最后一个参数为回调
	builtins["array_uintersect"] = func(e *Env, a []Value) (Value, error) {
		return phpArrayCbIntersect(e, a, true)
	}
	builtins["array_udiff"] = func(e *Env, a []Value) (Value, error) {
		return phpArrayCbIntersect(e, a, false)
	}
	builtins["array_intersect_ukey"] = func(e *Env, a []Value) (Value, error) {
		return phpArrayCbKey(e, a)
	}
	// array_is_list：键是否为 0..count-1 连续整数
	builtins["array_is_list"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray {
			return NewBool(false), nil
		}
		arr := deref(a[0])
		for i, k := range arr.Keys {
			if k != strconv.Itoa(i) {
				return NewBool(false), nil
			}
		}
		return NewBool(true), nil
	}
	// array_walk：值回调；phpgo 中回调按值传递（无法写穿原数组）
	builtins["array_walk"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 || a[0].Kind != KindArray {
			return NewBool(false), nil
		}
		arr := deref(a[0])
		cb := a[1]
		for _, k := range arr.Keys {
			var args []Value
			if len(a) >= 3 {
				args = []Value{arr.Arr[k], NewString(k), a[2]}
			} else {
				args = []Value{arr.Arr[k], NewString(k)}
			}
			_, _ = callCallable(e, cb, args)
		}
		return NewBool(true), nil
	}
	// array_walk_recursive：递归到子数组，只对非数组叶子调用回调
	builtins["array_walk_recursive"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 || a[0].Kind != KindArray {
			return NewBool(false), nil
		}
		arr := deref(a[0])
		cb := a[1]
		var walk func(v Value)
		walk = func(v Value) {
			if v.Kind != KindArray {
				return
			}
			for _, k := range v.Keys {
				item := v.Arr[k]
				if item.Kind == KindArray {
					walk(item)
					continue
				}
				var args []Value
				if len(a) >= 3 {
					args = []Value{item, NewString(k), a[2]}
				} else {
					args = []Value{item, NewString(k)}
				}
				_, _ = callCallable(e, cb, args)
			}
		}
		walk(arr)
		return NewBool(true), nil
	}
	// array_multisort(&$array1[, SORT_ASC|SORT_DESC[, SORT_*[, ...]], &$array2, ...])
	// 第一个数组为主排序列；返回 true/false。
	builtins["array_multisort"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewBool(false), nil
		}
		type col struct {
			arr Value
			dir int // +1 asc, -1 desc
			typ int // SORT_REGULAR/NUMERIC/STRING/NATURAL
		}
		var cols []col
		dir := 1
		typ := 0
		for _, arg := range a {
			v := deref(arg)
			switch {
			case v.Kind == KindInt && (v.Int == 3 || v.Int == 4):
				dir = 1
				if v.Int == 3 {
					dir = -1
				}
			case v.Kind == KindInt && (v.Int == 0 || v.Int == 1 || v.Int == 2 || v.Int == 5 || v.Int == 6 || v.Int == 8):
				typ = int(v.Int & 15)
			case v.Kind == KindArray:
				cols = append(cols, col{arr: v, dir: dir, typ: typ})
				dir = 1
				typ = 0
			}
		}
		if len(cols) == 0 {
			return NewBool(false), nil
		}
		n := len(cols[0].arr.Keys)
		idx := make([]int, n)
		for i := range idx {
			idx[i] = i
		}
		// 每列按原数组插入序取值（元素 i）
		vals := make([][]Value, len(cols))
		for ci, c := range cols {
			vals[ci] = make([]Value, 0, len(c.arr.Keys))
			for _, k := range c.arr.Keys {
				vals[ci] = append(vals[ci], c.arr.Arr[k])
			}
		}
		less := func(i, j int) bool {
			for ci, c := range cols {
				if i >= len(vals[ci]) || j >= len(vals[ci]) {
					return false
				}
				cmp := phpSortCompare(vals[ci][i], vals[ci][j], c.typ)
				if cmp != 0 {
					if c.dir < 0 {
						return cmp > 0
					}
					return cmp < 0
				}
			}
			return false
		}
		sort.SliceStable(idx, func(i, j int) bool { return less(idx[i], idx[j]) })
		// 回写每个数组（按排列重排并重新索引），若实参是引用则写回原变量
		for ci, c := range cols {
			reordered := NewArray()
			for _, id := range idx {
				if id < len(vals[ci]) {
					reordered.ArraySet(NewInt(int64(len(reordered.Keys))), vals[ci][id])
				}
			}
			// 找到原始实参位置
			origIdx := -1
			count := 0
			for ai, arg := range a {
				if deref(arg).Kind == KindArray {
					if count == ci {
						origIdx = ai
						break
					}
					count++
				}
			}
			if origIdx >= 0 {
				writeRef(e, a[origIdx], reordered)
			}
			_ = c
		}
		return NewBool(true), nil
	}
}

// phpArrayCbIntersect 回调比较值的差/交集（保留第一个数组的键）。
// intersect=true 时元素须在其余所有数组中按回调 == 命中；否则为差集。
func phpArrayCbIntersect(e *Env, a []Value, intersect bool) (Value, error) {
	if len(a) < 2 {
		return NewArray(), nil
	}
	cb := a[len(a)-1]
	arrays := a[:len(a)-1]
	first := deref(arrays[0])
	out := NewArray()
	for _, k := range first.Keys {
		v := first.Arr[k]
		hitAll := true
		for _, other := range arrays[1:] {
			o := deref(other)
			if o.Kind != KindArray {
				hitAll = false
				break
			}
			found := false
			for _, kk := range o.Keys {
				r, _ := callCallable(e, cb, []Value{v, o.Arr[kk]})
				if r.ToInt() == 0 {
					found = true
					break
				}
			}
			if !found {
				hitAll = false
				break
			}
		}
		if intersect && hitAll {
			out.ArraySet(NewString(k), v)
		}
		if !intersect && !hitAll {
			out.ArraySet(NewString(k), v)
		}
	}
	return out, nil
}

// phpArrayCbKey 回调比较键的交集（array_intersect_ukey）。
func phpArrayCbKey(e *Env, a []Value) (Value, error) {
	if len(a) < 2 {
		return NewArray(), nil
	}
	cb := a[len(a)-1]
	arrays := a[:len(a)-1]
	first := deref(arrays[0])
	out := NewArray()
	for _, k := range first.Keys {
		hitAll := true
		for _, other := range arrays[1:] {
			o := deref(other)
			if o.Kind != KindArray {
				hitAll = false
				break
			}
			found := false
			for _, kk := range o.Keys {
				r, _ := callCallable(e, cb, []Value{NewString(k), NewString(kk)})
				if r.ToInt() == 0 {
					found = true
					break
				}
			}
			if !found {
				hitAll = false
				break
			}
		}
		if hitAll {
			out.ArraySet(NewString(k), first.Arr[k])
		}
	}
	return out, nil
}

// phpSortCompare 按排序类型比较两个 PHP 值，返回 <0 / 0 / >0。
func phpSortCompare(x, y Value, typ int) int {
	switch typ {
	case 1: // SORT_NUMERIC
		f1, f2 := x.ToFloat(), y.ToFloat()
		if f1 < f2 {
			return -1
		}
		if f1 > f2 {
			return 1
		}
		return 0
	case 2, 5: // SORT_STRING / SORT_LOCALE_STRING
		return strings.Compare(x.ToString(), y.ToString())
	case 6: // SORT_NATURAL
		return naturalCompare(x.ToString(), y.ToString())
	default: // SORT_REGULAR：数字按数值比，其余按字符串
		return phpRegularCompare(x, y)
	}
}

// phpRegularCompare 模拟 PHP SORT_REGULAR：两值可转数字且非 null/bool/string
// 语义下的数字比较（简化为：双方数值均为数字型或数字字符串时按数值比，否则字符串比）。
func phpRegularCompare(x, y Value) int {
	bothNum := func(v Value) bool {
		if v.Kind == KindInt || v.Kind == KindFloat {
			return true
		}
		if v.Kind == KindString {
			s := strings.TrimSpace(v.Str)
			if s == "" {
				return false
			}
			_, err1 := strconv.ParseInt(s, 10, 64)
			_, err2 := strconv.ParseFloat(s, 64)
			return err1 == nil || err2 == nil
		}
		return false
	}
	if bothNum(x) && bothNum(y) {
		f1, f2 := x.ToFloat(), y.ToFloat()
		if f1 < f2 {
			return -1
		}
		if f1 > f2 {
			return 1
		}
		return 0
	}
	return strings.Compare(x.ToString(), y.ToString())
}
