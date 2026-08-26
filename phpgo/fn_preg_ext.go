package phpgo

func init() {
	builtins["preg_replace_callback"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 3 {
			return NewNull(), nil
		}
		pattern := a[0].ToString()
		// 回调函数名或闭包
		callbackName := a[1].ToString()
		subj := a[2].ToString()

		re, err := compilePHPRegex(pattern)
		if err != nil {
			return NewString(subj), nil
		}

		result := re.ReplaceAllStringFunc(subj, func(match string) string {
			subs := re.FindStringSubmatch(match)
			// 构建参数数组
			args := make([]Value, len(subs))
			for i, s := range subs {
				args[i] = NewString(s)
			}
			var ret Value
			if bf, ok := builtins[callbackName]; ok {
				ret, _ = bf(e, args)
			} else if fn, ok := e.funcs[callbackName]; ok {
				ret, _ = e.callUserFuncValues(fn, args)
			}
			return ret.ToString()
		})
		return NewString(result), nil
	}

	builtins["preg_split"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewArray(), nil
		}
		pattern := a[0].ToString()
		subj := a[1].ToString()
		re, err := compilePHPRegex(pattern)
		if err != nil {
			return NewArray(), nil
		}
		parts := re.Split(subj, -1)
		result := NewArray()
		for i, p := range parts {
			result.ArraySet(NewInt(int64(i)), NewString(p))
		}
		return result, nil
	}

	builtins["preg_replace_callback_array"] = func(e *Env, a []Value) (Value, error) {
		// 简化：不支持
		if len(a) < 2 {
			return NewNull(), nil
		}
		return a[1], nil
	}
}
