package phpgo

// OO 反射函数：class_exists / interface_exists / trait_exists / method_exists /
// property_exists / get_class / get_parent_class / get_called_class / is_a /
// is_subclass_of / get_object_vars / get_class_methods / get_class_vars / get_declared_classes。
//
// 限制说明：phpgo 的 ClassDecl 不记录继承关系，is_a/is_subclass_of 退化为「类名相等」判断；
// 内建类（DateTime/Exception 等）按最小方法集处理。

import (
	"sort"
	"strings"
)

// coreClassMethods 内建/核心类及其可用方法（小写比较）。
var coreClassMethods = map[string][]string{
	"DateTime":       {"__construct", "createfromformat", "format", "modify", "gettimestamp", "settimestamp"},
	"DateTimeImmutable": {"__construct", "createfromformat", "format", "modify", "gettimestamp", "settimestamp"},
	"Exception":      {"__construct"},
	"RuntimeException": {"__construct"},
	"Error":          {"__construct"},
	"Throwable":      {},
	"stdClass":       {},
	"Closure":        {},
}

// coreClasses 类名 → 是否作为核心类存在
var coreClasses = map[string]bool{
	"DateTime": true, "DateTimeImmutable": true, "DateTimeInterface": true,
	"Exception": true, "RuntimeException": true, "LogicException": true,
	"InvalidArgumentException": true, "DomainException": true, "RangeException": true,
	"OutOfBoundsException": true, "OutOfRangeException": true, "OverflowException": true,
	"UnderflowException": true, "LengthException": true, "UnexpectedValueException": true,
	"Error": true, "TypeError": true, "ValueError": true, "Throwable": true,
	"stdClass": true, "Closure": true, "JsonException": true, "PDOException": true,
	"ArrayObject": true, "Generator": true,
}

// findClass 按类名（忽略大小写）在用户类中查找。
func (e *Env) findClass(name string) (*ClassDecl, string, bool) {
	if cls, ok := e.classes[name]; ok {
		return cls, name, true
	}
	for k, cls := range e.classes {
		if strings.EqualFold(k, name) {
			return cls, k, true
		}
	}
	return nil, "", false
}

// findMethod 在类中查找方法（忽略大小写）。
func findClassMethod(cls *ClassDecl, method string) bool {
	for _, m := range cls.Methods {
		if strings.EqualFold(m.Name, method) {
			return true
		}
	}
	return false
}

// classOfValue 取对象/类名参数的类名字符串；取不到返回 ""。
func classOfValue(v Value) string {
	v = deref(v)
	if v.Kind == KindObject && v.Object != nil {
		return v.Object.ClassName
	}
	return ""
}

func init() {
	builtins["class_exists"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		name := a[0].ToString()
		if _, ok := e.classes[name]; ok {
			return NewBool(true), nil
		}
		if _, _, ok := e.findClass(name); ok {
			return NewBool(true), nil
		}
		// 内建核心类
		return NewBool(coreClasses[name]), nil
	}
	builtins["interface_exists"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		name := a[0].ToString()
		if _, _, ok := e.findClass(name); ok {
			return NewBool(true), nil
		}
		// PHP 中接口也可通过 interface_exists 判断；phpgo 不区分接口与类，返回类存在性
		return NewBool(coreClasses[name]), nil
	}
	builtins["trait_exists"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		_, _, ok := e.findClass(a[0].ToString())
		return NewBool(ok), nil
	}
	builtins["method_exists"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		method := a[1].ToString()
		clsName := classOfValue(a[0])
		if clsName == "" {
			// 字符串类名
			clsName = a[0].ToString()
			if _, ok := coreClasses[clsName]; !ok {
				cls, _, ok2 := e.findClass(clsName)
				if !ok2 {
					return NewBool(false), nil
				}
				return NewBool(findClassMethod(cls, method)), nil
			}
		}
		if cls, _, ok := e.findClass(clsName); ok {
			return NewBool(findClassMethod(cls, method)), nil
		}
		if methods, ok := coreClassMethods[clsName]; ok {
			for _, m := range methods {
				if strings.EqualFold(m, method) {
					return NewBool(true), nil
				}
			}
		}
		return NewBool(false), nil
	}
	builtins["property_exists"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		prop := a[1].ToString()
		target := deref(a[0])
		// 对象：查已设置属性
		if target.Kind == KindObject && target.Object != nil {
			if _, ok := target.Object.Properties[prop]; ok {
				return NewBool(true), nil
			}
			clsName := target.Object.ClassName
			if cls, _, ok := e.findClass(clsName); ok {
				for _, p := range cls.Properties {
					if strings.EqualFold(p.Name, prop) {
						return NewBool(true), nil
					}
				}
			}
			return NewBool(false), nil
		}
		// 类名
		if cls, _, ok := e.findClass(target.ToString()); ok {
			for _, p := range cls.Properties {
				if strings.EqualFold(p.Name, prop) {
					return NewBool(true), nil
				}
			}
		}
		return NewBool(false), nil
	}
	// get_class($object = null)：对象→类名；方法内调用→当前类名；否则 false
	builtins["get_class"] = func(e *Env, a []Value) (Value, error) {
		if len(a) > 0 {
			cn := classOfValue(a[0])
			if cn == "" {
				return NewBool(false), nil
			}
			return NewString(cn), nil
		}
		if cur, ok := e.vars["__current_class__"]; ok && cur.Kind == KindString {
			return cur, nil
		}
		return NewBool(false), nil
	}
	builtins["get_parent_class"] = func(e *Env, a []Value) (Value, error) {
		// phpgo 不支持类继承，统一返回 false（PHP 无父类时也返回 false）
		return NewBool(false), nil
	}
	builtins["get_called_class"] = func(e *Env, a []Value) (Value, error) {
		if cur, ok := e.vars["__current_class__"]; ok && cur.Kind == KindString {
			return cur, nil
		}
		return NewBool(false), nil
	}
	// is_a($object_or_class, $class, $allow_string = false)
	builtins["is_a"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		class := a[1].ToString()
		obj := deref(a[0])
		cn := classOfValue(obj)
		if cn != "" {
			return NewBool(strings.EqualFold(cn, class)), nil
		}
		if obj.Kind == KindString {
			allowString := false
			if len(a) >= 3 {
				allowString = a[2].ToBool()
			}
			if allowString {
				if _, ok := e.classes[obj.Str]; ok {
					return NewBool(strings.EqualFold(obj.Str, class)), nil
				}
			}
		}
		return NewBool(false), nil
	}
	// is_subclass_of($object_or_class, $class)：无继承元数据，退化为类名相等
	builtins["is_subclass_of"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		class := a[1].ToString()
		if _, _, ok := e.findClass(class); !ok && !coreClasses[class] {
			return NewBool(false), nil
		}
		obj := deref(a[0])
		cn := classOfValue(obj)
		if cn == "" {
			cn = obj.ToString()
		}
		if strings.EqualFold(cn, class) {
			return NewBool(false), nil // PHP: 自身类不算其子类
		}
		// 无法证明继承关系，返回 false（保守、不误报）
		return NewBool(false), nil
	}
	// get_object_vars($object)：返回对象属性数组（phpgo 不区分可见性，返回全部已设置属性）
	builtins["get_object_vars"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewArray(), nil
		}
		obj := deref(a[0])
		if obj.Kind != KindObject || obj.Object == nil {
			return NewArray(), nil
		}
		out := NewArray()
		for _, k := range obj.Object.PropKeysInOrder() {
			out.ArraySet(NewString(k), obj.Object.Properties[k])
		}
		return out, nil
	}
	// get_class_methods($class_or_object)：方法名数组
	builtins["get_class_methods"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewArray(), nil
		}
		clsName := classOfValue(a[0])
		if clsName == "" {
			clsName = a[0].ToString()
		}
		out := NewArray()
		if methods, ok := coreClassMethods[clsName]; ok {
			for _, m := range methods {
				if m == "__construct" {
					continue
				}
				out.ArraySet(NewInt(int64(len(out.Keys))), NewString(m))
			}
			return out, nil
		}
		if cls, _, ok := e.findClass(clsName); ok {
			for _, m := range cls.Methods {
				out.ArraySet(NewInt(int64(len(out.Keys))), NewString(m.Name))
			}
		}
		return out, nil
	}
	// get_class_vars($class)：返回类属性默认值（含默认 null 的属性）
	builtins["get_class_vars"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewArray(), nil
		}
		cls, _, ok := e.findClass(a[0].ToString())
		if !ok {
			return NewArray(), nil
		}
		out := NewArray()
		for _, p := range cls.Properties {
			if p.Default != nil {
				v, err := e.evalExpr(p.Default)
				if err != nil {
					out.ArraySet(NewString(p.Name), NewNull())
					continue
				}
				out.ArraySet(NewString(p.Name), v)
			} else {
				out.ArraySet(NewString(p.Name), NewNull())
			}
		}
		return out, nil
	}
	// get_declared_classes()：已声明类列表（用户类 + 内建类，排序后返回）
	builtins["get_declared_classes"] = func(e *Env, a []Value) (Value, error) {
		names := make(map[string]bool)
		for k := range e.classes {
			names[k] = true
		}
		for k := range coreClasses {
			names[k] = true
		}
		sorted := make([]string, 0, len(names))
		for k := range names {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		out := NewArray()
		for _, n := range sorted {
			out.ArraySet(NewInt(int64(len(out.Keys))), NewString(n))
		}
		return out, nil
	}
}
