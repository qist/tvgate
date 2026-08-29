package phpgo

// 函数调用与常量相关：call_user_func(_array) / function_exists / defined / constant / extract。
// callCallable 也供 array_map/array_filter/usort 等回调型内置函数复用。
func init() {
	builtins["call_user_func"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewNull(), nil
		}
		return callCallable(e, a[0], a[1:])
	}
	builtins["call_user_func_array"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewNull(), nil
		}
		var args []Value
		if a[1].Kind == KindArray {
			for _, k := range a[1].Keys {
				args = append(args, a[1].Arr[k])
			}
		}
		return callCallable(e, a[0], args)
	}
	builtins["function_exists"] = func(e *Env, a []Value) (Value, error) {
		name := a[0].ToString()
		if _, ok := builtins[name]; ok {
			return NewBool(true), nil
		}
		_, ok := e.funcs[name]
		return NewBool(ok), nil
	}
	builtins["defined"] = func(e *Env, a []Value) (Value, error) {
		_, ok := e.consts[a[0].ToString()]
		return NewBool(ok), nil
	}
	builtins["constant"] = func(e *Env, a []Value) (Value, error) {
		if v, ok := e.consts[a[0].ToString()]; ok {
			return v, nil
		}
		return NewNull(), nil
	}
	// extract：把关联数组导入当前符号表；默认覆盖同名变量
	builtins["extract"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray {
			return NewInt(0), nil
		}
		flags := 0
		if len(a) >= 2 {
			flags = int(a[1].ToInt())
		}
		prefix := ""
		if len(a) >= 3 {
			prefix = a[2].ToString()
		}
		count := int64(0)
		for _, k := range a[0].Keys {
			name := k
			if prefix != "" && (flags == 3 || flags == 5 || flags == 2) { // EXTR_PREFIX_ALL / _IF_EXISTS / _SAME
				name = prefix + "_" + name
			}
			if !isValidVarName(name) {
				continue
			}
			if flags == 1 { // EXTR_SKIP
				if _, exists := e.vars[name]; exists {
					continue
				}
			}
			if (flags == 2 || flags == 5) && prefix == "" {
				if _, exists := e.vars[name]; !exists {
					continue
				}
			}
			if flags == 6 { // EXTR_IF_EXISTS
				if _, exists := e.vars[name]; !exists {
					continue
				}
			}
			e.vars[name] = a[0].Arr[k]
			e.globals[name] = a[0].Arr[k]
			count++
		}
		return NewInt(count), nil
	}
}

// isValidVarName 判断是否合法的 PHP 变量名
func isValidVarName(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (i > 0 && c >= '0' && c <= '9') {
			continue
		}
		return false
	}
	return true
}

// callCallable 调用可调用对象：字符串函数名（内置或用户函数）或 [对象|类名, 方法名] 数组
func callCallable(e *Env, callable Value, args []Value) (Value, error) {
	if callable.Kind == KindString {
		name := callable.Str
		if bf, ok := builtins[name]; ok {
			return bf(e, args)
		}
		if fn, ok := e.funcs[name]; ok {
			return e.callUserFuncValues(fn, args)
		}
		return NewNull(), nil
	}
	if callable.Kind == KindArray && len(callable.Keys) >= 2 {
		// 闭包：{__closure_name, __captured}
		if cn := callable.ArrayGet(NewString("__closure_name")); cn.Kind == KindString {
			fn, ok := e.funcs[cn.Str]
			if !ok {
				return NewNull(), nil
			}
			// 恢复捕获变量到当前作用域（callUserFuncValues 会一并保存/还原）
			if capV := callable.ArrayGet(NewString("__captured")); capV.Kind == KindArray {
				for _, k := range capV.Keys {
					e.vars[k] = capV.Arr[k]
				}
			}
			return e.callUserFuncValues(fn, args)
		}
		target := callable.Arr[callable.Keys[0]]
		methodName := callable.Arr[callable.Keys[1]].ToString()
		if target.Kind == KindObject {
			cls, ok := e.classes[target.Object.ClassName]
			if !ok {
				return NewNull(), nil
			}
			for _, m := range cls.Methods {
				if m.Name == methodName {
					return e.callMethodValues(m, target, e.vars["__current_class__"], args)
				}
			}
		} else {
			className := target.ToString()
			if cls, ok := e.classes[className]; ok {
				for _, m := range cls.Methods {
					if m.Name == methodName {
						return e.callMethodValues(m, e.vars["this"], NewString(className), args)
					}
				}
			}
		}
	}
	return NewNull(), nil
}

// callMethodValues 用值参数调用对象/类方法（设置 $this 与 __current_class__，调用后恢复作用域）
func (e *Env) callMethodValues(fn *FuncDecl, thisVal, curClass Value, vs []Value) (Value, error) {
	saved := map[string]Value{}
	for k, v := range e.vars {
		if k == "this" || k == "__current_class__" {
			continue
		}
		saved[k] = v
	}
	oldThis := e.vars["this"]
	oldClass := e.vars["__current_class__"]
	e.vars = map[string]Value{}
	e.vars["this"] = thisVal
	e.vars["__current_class__"] = curClass
	defer func() {
		e.vars = saved
		e.vars["this"] = oldThis
		e.vars["__current_class__"] = oldClass
	}()
	for i, p := range fn.Params {
		if i < len(vs) {
			e.vars[p.Name] = vs[i].Clone()
		} else if p.Default != nil {
			v, err := e.evalExpr(p.Default)
			if err != nil {
				return v, err
			}
			e.vars[p.Name] = v
		} else {
			e.vars[p.Name] = NewNull()
		}
	}
	r, err := e.execBlock(fn.Body)
	if err != nil {
		return NewNull(), err
	}
	if r.flow == cfReturn {
		return r.val, nil
	}
	return NewNull(), nil
}
