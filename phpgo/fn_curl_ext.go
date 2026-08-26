package phpgo

// curl_setopt_array 完整实现 + curl_getinfo 扩展 + CURLOPT_RETURNTRANSFER
// 覆盖 funcs.go 中的简化版本（后注册的 init() 覆盖先注册的）

func init() {
	builtins["curl_setopt_array"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 || a[0].Kind != KindArray || a[1].Kind != KindArray {
			return NewBool(false), nil
		}
		h := a[0]
		for _, k := range a[1].Keys {
			h.ArraySet(NewString(k), a[1].Arr[k])
		}
		return NewBool(true), nil
	}

	builtins["curl_getinfo"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray {
			return NewArray(), nil
		}
		info := NewArray()
		info.ArraySet(NewString("http_code"), a[0].ArrayGet(NewString("__http_code")))
		info.ArraySet(NewString("effective_url"), a[0].ArrayGet(NewString("__effective_url")))
		info.ArraySet(NewString("content_type"), a[0].ArrayGet(NewString("__content_type")))
		// 单一选项模式
		if len(a) >= 2 {
			return info.ArrayGet(a[1]), nil
		}
		return info, nil
	}

	builtins["curl_errno"] = func(e *Env, a []Value) (Value, error) {
		return NewInt(0), nil
	}

	builtins["curl_reset"] = func(e *Env, a []Value) (Value, error) {
		return NewNull(), nil
	}

	builtins["curl_multi_init"] = func(e *Env, a []Value) (Value, error) {
		return NewArray(), nil
	}
	builtins["curl_multi_add_handle"] = func(e *Env, a []Value) (Value, error) {
		return NewInt(0), nil
	}
	builtins["curl_multi_exec"] = func(e *Env, a []Value) (Value, error) {
		return NewInt(0), nil
	}
	builtins["curl_multi_getcontent"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray {
			return NewString(""), nil
		}
		return a[0].ArrayGet(NewString("__response")), nil
	}
	builtins["curl_multi_info_read"] = func(e *Env, a []Value) (Value, error) {
		return NewBool(false), nil
	}
	builtins["curl_multi_select"] = func(e *Env, a []Value) (Value, error) {
		return NewInt(0), nil
	}
	builtins["curl_multi_remove_handle"] = func(e *Env, a []Value) (Value, error) {
		return NewInt(0), nil
	}
	builtins["curl_multi_close"] = func(e *Env, a []Value) (Value, error) {
		return NewNull(), nil
	}
}
