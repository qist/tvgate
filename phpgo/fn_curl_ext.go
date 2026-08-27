package phpgo

// curl 扩展函数（不与 funcs.go 重复注册）
// curl_setopt_array 和 curl_getinfo 在 funcs.go 中注册

func init() {
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
