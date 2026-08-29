package phpgo

import (
	"strconv"
	"strings"
)

// 变量/类型相关函数
func init() {
	builtins["is_object"] = func(e *Env, a []Value) (Value, error) {
		return NewBool(len(a) > 0 && a[0].Kind == KindObject), nil
	}
	builtins["is_scalar"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewBool(false), nil
		}
		switch a[0].Kind {
		case KindBool, KindInt, KindFloat, KindString:
			return NewBool(true), nil
		}
		return NewBool(false), nil
	}
	builtins["is_iterable"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewBool(false), nil
		}
		return NewBool(a[0].Kind == KindArray || a[0].Kind == KindObject), nil
	}
	builtins["is_countable"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewBool(false), nil
		}
		return NewBool(a[0].Kind == KindArray), nil
	}
	builtins["is_resource"] = func(e *Env, a []Value) (Value, error) {
		return NewBool(len(a) > 0 && a[0].Kind == KindResource), nil
	}
	builtins["is_callable"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		v := deref(a[0])
		switch v.Kind {
		case KindString:
			if _, ok := builtins[v.Str]; ok {
				return NewBool(true), nil
			}
			_, ok := e.funcs[v.Str]
			return NewBool(ok), nil
		case KindArray:
			if len(v.Keys) >= 2 {
				target := v.Arr[v.Keys[0]]
				method := v.Arr[v.Keys[1]].ToString()
				var className string
				if target.Kind == KindObject {
					className = target.Object.ClassName
				} else {
					className = target.ToString()
				}
				if cls, ok := e.classes[className]; ok {
					for _, m := range cls.Methods {
						if m.Name == method {
							return NewBool(true), nil
						}
					}
				}
			}
			return NewBool(false), nil
		}
		return NewBool(false), nil
	}
	// settype：按引用转换变量类型
	builtins["settype"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		v := deref(a[0])
		switch a[1].ToString() {
		case "int", "integer":
			v = NewInt(v.ToInt())
		case "float", "double":
			v = NewFloat(v.ToFloat())
		case "string":
			v = NewString(v.ToString())
		case "bool", "boolean":
			v = NewBool(v.ToBool())
		case "array":
			if v.Kind != KindArray {
				nv := NewArray()
				if v.Kind != KindNull {
					nv.ArraySet(NewInt(0), v)
				}
				v = nv
			}
		case "null":
			v = NewNull()
		default:
			return NewBool(false), nil
		}
		writeRef(e, a[0], v)
		return NewBool(true), nil
	}
	// var_export：导出 PHP 代码表示；第二个参数为 true 时返回字符串而非输出
	builtins["var_export"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewNull(), nil
		}
		s := phpVarExport(a[0])
		if len(a) >= 2 && a[1].ToBool() {
			return NewString(s), nil
		}
		e.writeOutput(s)
		return NewNull(), nil
	}
	// get_debug_type：返回类型名（PHP 8.0+，简化）
	builtins["get_debug_type"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewString("null"), nil
		}
		return NewString(kindName(a[0].Kind)), nil
	}
}

// phpVarExport 生成 var_export 的 PHP 代码表示
func phpVarExport(v Value) string {
	switch v.Kind {
	case KindNull:
		return "NULL"
	case KindBool:
		if v.Bool {
			return "true"
		}
		return "false"
	case KindInt:
		return strconv.FormatInt(v.Int, 10)
	case KindFloat:
		return strconv.FormatFloat(v.Float, 'g', -1, 64)
	case KindString:
		return "'" + strings.ReplaceAll(v.Str, "'", "\\'") + "'"
	case KindArray:
		var b strings.Builder
		b.WriteString("array (\n")
		for _, k := range v.Keys {
			if isNumericKey(k) {
				b.WriteString("  " + k + " => ")
			} else {
				b.WriteString("  '" + strings.ReplaceAll(k, "'", "\\'") + "' => ")
			}
			b.WriteString(phpVarExport(v.Arr[k]))
			b.WriteString(",\n")
		}
		b.WriteString(")")
		return b.String()
	}
	return v.ToString()
}

// kindName 返回 Kind 的调试名称
func kindName(k Kind) string {
	switch k {
	case KindNull:
		return "null"
	case KindBool:
		return "bool"
	case KindInt:
		return "int"
	case KindFloat:
		return "float"
	case KindString:
		return "string"
	case KindArray:
		return "array"
	case KindObject:
		return "object"
	case KindResource:
		return "resource"
	}
	return "unknown"
}
