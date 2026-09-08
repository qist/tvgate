package phpgo

// 用户函数实参栈基础设施 + func_get_args / func_num_args / func_get_arg。
//
// PHP 语义要点：
//   - func_get_args() 返回当前用户函数「绑定后的全部实参」（含默认值填充的参数）；
//   - 只有在用户函数/方法体内调用才有意义，顶层调用返回 false（func_num_args 为 -1）；
//   - 返回值是实参当前值的副本（引用形参解引用后取当前值）。

// pushCallFrame 在进入用户函数执行体前，把当前绑定好的形参值压入实参栈。
// params 为函数形参列表；值取自 e.vars（此时已完成参数绑定，含默认值）。
func (e *Env) pushCallFrame(params []FuncParam) {
	frame := make([]Value, 0, len(params))
	for _, p := range params {
		v := e.vars[p.Name]
		if p.Variadic && v.Kind == KindArray {
			// ...$rest：展开为独立参数
			for _, k := range v.Keys {
				frame = append(frame, e.resolveRefValue(v.Arr[k]))
			}
			continue
		}
		frame = append(frame, e.resolveRefValue(v))
	}
	e.callArgs = append(e.callArgs, frame)
}

// popCallFrame 函数返回时弹出实参帧（配合 defer 使用）。
func (e *Env) popCallFrame() {
	if n := len(e.callArgs); n > 0 {
		e.callArgs = e.callArgs[:n-1]
	}
}

// topCallFrame 返回当前用户函数实参帧；不在函数体内返回 nil。
func (e *Env) topCallFrame() []Value {
	if n := len(e.callArgs); n > 0 {
		return e.callArgs[n-1]
	}
	return nil
}

// resolveRefValue 把 KindRef 解引用为实参当前值；非引用直接返回。
func (e *Env) resolveRefValue(v Value) Value {
	if v.Kind == KindRef {
		if v.Ref != nil {
			return v.Ref.value(e)
		}
		if v.RefVal != nil {
			return *v.RefVal
		}
		return NewNull()
	}
	return v
}

func init() {
	builtins["func_get_args"] = func(e *Env, a []Value) (Value, error) {
		frame := e.topCallFrame()
		if frame == nil {
			return NewBool(false), nil
		}
		out := NewArray()
		for _, v := range frame {
			out.ArraySet(NewInt(int64(len(out.Keys))), e.resolveRefValue(v).Clone())
		}
		return out, nil
	}
	builtins["func_num_args"] = func(e *Env, a []Value) (Value, error) {
		frame := e.topCallFrame()
		if frame == nil {
			return NewInt(-1), nil
		}
		return NewInt(int64(len(frame))), nil
	}
	builtins["func_get_arg"] = func(e *Env, a []Value) (Value, error) {
		frame := e.topCallFrame()
		if frame == nil || len(a) < 1 {
			return NewBool(false), nil
		}
		i := a[0].ToInt()
		if i < 0 || i >= int64(len(frame)) {
			return NewBool(false), nil
		}
		return e.resolveRefValue(frame[i]).Clone(), nil
	}
}
