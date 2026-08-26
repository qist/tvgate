package phpgo

func init() {
	// gettype：返回变量类型的字符串表示
	builtins["gettype"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewString("NULL"), nil
		}
		switch a[0].Kind {
		case KindNull:
			return NewString("NULL"), nil
		case KindBool:
			return NewString("boolean"), nil
		case KindInt:
			return NewString("integer"), nil
		case KindFloat:
			return NewString("double"), nil
		case KindString:
			return NewString("string"), nil
		case KindArray:
			return NewString("array"), nil
		default:
			return NewString("unknown"), nil
		}
	}

	// boolval：转布尔
	builtins["boolval"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewBool(false), nil
		}
		return NewBool(a[0].ToBool()), nil
	}

	// print：语言结构，输出并返回 1
	builtins["print"] = func(e *Env, a []Value) (Value, error) {
		if len(a) > 0 {
			e.echoOut.WriteString(a[0].ToString())
		}
		return NewInt(1), nil
	}

	// rsort：数组按值逆序排序（重置数字键）
	builtins["rsort"] = func(e *Env, a []Value) (Value, error) {
		return sortArray(e, a, false, true)
	}
	// isset / empty / unset 的语言结构版本已在 parser/eval 处理；
	// 这里兜底注册，使以函数形式调用（如 call_user_func）也能工作。
	builtins["isset"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewBool(false), nil
		}
		return NewBool(a[0].Kind != KindNull), nil
	}
	builtins["empty"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewBool(true), nil
		}
		return NewBool(isEmptyValue(a[0])), nil
	}
	builtins["unset"] = func(e *Env, a []Value) (Value, error) {
		return NewNull(), nil
	}
}

// isEmptyValue 判断 PHP empty() 语义
func isEmptyValue(v Value) bool {
	switch v.Kind {
	case KindNull:
		return true
	case KindBool:
		return !v.Bool
	case KindInt:
		return v.Int == 0
	case KindFloat:
		return v.Float == 0
	case KindString:
		return v.Str == "" || v.Str == "0"
	case KindArray:
		return len(v.Keys) == 0
	default:
		return false
	}
}

// sortArray 公共排序实现（被 sort/asort/ksort/rsort 复用）
// byKey=true 按键排序；byVal=true 保留键名（关联数组语义）；desc=true 逆序
func sortArray(e *Env, a []Value, byKey, desc bool) (Value, error) {
	if len(a) == 0 || a[0].Kind != KindArray {
		return NewBool(false), nil
	}
	arr := a[0]
	type kv struct {
		k string
		v Value
	}
	var items []kv
	for _, k := range arr.Keys {
		items = append(items, kv{k, arr.Arr[k]})
	}
	less := func(i, j int) bool {
		var a1, a2 string
		if byKey {
			a1, a2 = items[i].k, items[j].k
		} else {
			a1, a2 = items[i].v.ToString(), items[j].v.ToString()
		}
		if desc {
			return a1 > a2
		}
		return a1 < a2
	}
	// 简单插入排序（PHP 数组规模小，足够）
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && less(j, j-1); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
	out := NewArray()
	if byKey {
		for _, it := range items {
			out.ArraySet(NewString(it.k), it.v)
		}
	} else {
		// sort/rsort：重置数字索引
		for _, it := range items {
			out.ArraySet(NewInt(int64(len(out.Keys))), it.v)
		}
	}
	if len(a) > 0 {
		// 写回原变量（若引用）
		writeRef(e, a[0], out)
	}
	return NewBool(true), nil
}
