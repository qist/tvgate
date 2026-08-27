// Package phpgo 是一个纯 Go 实现的 PHP 子集解释器（Worker Runtime 雏形）。
// 目标：在不引入 CGO / 不依赖 PHP 解释器的前提下，用 Go 原生实现 TVGate
// PHP 模块所需的 PHP 语法与内置函数子集，使 /www 中的脚本可被解释执行。
// 当前为 PoC：聚焦覆盖 4gtv.php 用到的语法与函数。
package phpgo

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Kind 表示 PHP 值的类型
type Kind int

const (
	KindNull Kind = iota
	KindBool
	KindInt
	KindFloat
	KindString
	KindArray
	KindRef // 变量引用（&$var 形参）
	KindResource // 资源（文件句柄 / 秘钥 / 流等，存任意 Go 对象）
)

// Value 是简化版 PHP zval：支持 null/bool/int/float/string/array。
// 关联数组与索引数组统一用 map[string]Value 表示（key 为 string 形式），
// 索引数组的 key 为十进制整数字符串，保证顺序通过插入序维护。
type Value struct {
	Kind  Kind
	Bool  bool
	Int   int64
	Float float64
	Str   string
	// Arr 仅用于数组；keys 维护插入顺序（PHP 数组保序）
	Arr  map[string]Value
	Keys []string
	// Ref 用于 &$var 引用参数
	Ref assignable
	// RefVal 缓存引用的当前值（在 callFunc 中填充，供 ToString 等无 Env 方法使用）
	RefVal *Value
	// Resource 用于资源类型（文件/流/秘钥等 opaque Go 对象）
	Resource interface{}
}

// NewRef 创建引用值
func NewRef(r assignable) Value { return Value{Kind: KindRef, Ref: r} }

// NewResource 创建资源值
func NewResource(r interface{}) Value { return Value{Kind: KindResource, Resource: r} }

// NewNull 返回 null 值
func NewNull() Value { return Value{Kind: KindNull} }

// NewBool ...
func NewBool(b bool) Value { return Value{Kind: KindBool, Bool: b} }

// NewInt ...
func NewInt(i int64) Value { return Value{Kind: KindInt, Int: i} }

// NewFloat ...
func NewFloat(f float64) Value { return Value{Kind: KindFloat, Float: f} }

// NewString ...
func NewString(s string) Value { return Value{Kind: KindString, Str: s} }

// NewArray 创建空数组
func NewArray() Value {
	return Value{Kind: KindArray, Arr: map[string]Value{}, Keys: nil}
}

// IsNull 是否 null（含未定义变量语义）
func (v Value) IsNull() bool { return v.Kind == KindNull }

// ToBool PHP 真假值语义
func (v Value) ToBool() bool {
	switch v.Kind {
	case KindNull:
		return false
	case KindBool:
		return v.Bool
	case KindInt:
		return v.Int != 0
	case KindFloat:
		return v.Float != 0
case KindString:
return v.Str != "" && v.Str != "0"
case KindRef:
if v.RefVal != nil {
return v.RefVal.ToBool()
}
return false
case KindArray:
		return len(v.Keys) > 0
	}
	return false
}

// ToFloat 转浮点数
func (v Value) ToFloat() float64 {
	switch v.Kind {
	case KindBool:
		if v.Bool {
			return 1
		}
		return 0
	case KindInt:
		return float64(v.Int)
	case KindFloat:
		return v.Float
case KindString:
f, _ := strconv.ParseFloat(strings.TrimSpace(v.Str), 64)
return f
case KindRef:
if v.RefVal != nil {
return v.RefVal.ToFloat()
}
return 0
}
	return 0
}

// ToInt 转整数（简化）
func (v Value) ToInt() int64 {
	switch v.Kind {
	case KindBool:
		if v.Bool {
			return 1
		}
		return 0
	case KindInt:
		return v.Int
	case KindFloat:
		return int64(v.Float)
case KindString:
var n int64
fmt.Sscanf(v.Str, "%d", &n)
return n
case KindRef:
if v.RefVal != nil {
return v.RefVal.ToInt()
}
return 0
}
	return 0
}

// ToString PHP 字符串转换语义
func (v Value) ToString() string {
	switch v.Kind {
	case KindNull:
		return ""
	case KindBool:
		if v.Bool {
			return "1"
		}
		return ""
	case KindInt:
		return fmt.Sprintf("%d", v.Int)
	case KindFloat:
		// 对齐 PHP float → string：整数值不显示小数点，非整数用普通格式（不用科学记数法）
		if v.Float == math.Trunc(v.Float) && v.Float >= -1e15 && v.Float <= 1e15 {
			return fmt.Sprintf("%d", int64(v.Float))
		}
		return strconv.FormatFloat(v.Float, 'f', -1, 64)
	case KindString:
		return v.Str
	case KindRef:
		if v.RefVal != nil {
			return v.RefVal.ToString()
		}
		return ""
	case KindArray:
		return "Array"
	}
	return ""
}

// ArrayGet 按 key 取数组元素（自动字符串化 key）
func (v Value) ArrayGet(key Value) Value {
	// 字符串索引：$str[$i] 返回第 i 个字符
	if v.Kind == KindString {
		idx := key.ToInt()
		if idx < 0 || idx >= int64(len(v.Str)) {
			return NewString("")
		}
		return NewString(string(v.Str[idx]))
	}
	if v.Kind != KindArray {
		return NewNull()
	}
	k := key.ToString()
	if e, ok := v.Arr[k]; ok {
		return e
	}
	return NewNull()
}

// ArraySet 设置数组元素（维护插入序）
func (v *Value) ArraySet(key Value, val Value) {
	if v.Kind != KindArray {
		v.Kind = KindArray
		v.Arr = map[string]Value{}
		v.Keys = nil
	}
	k := key.ToString()
	if _, ok := v.Arr[k]; !ok {
		v.Keys = append(v.Keys, k)
	}
	v.Arr[k] = val
}

// IsArrayKeyExists 判断关联数组是否含 key
func (v Value) IsArrayKeyExists(key Value) bool {
	if v.Kind != KindArray {
		return false
	}
	_, ok := v.Arr[key.ToString()]
	return ok
}

// ArrayUnset 删除数组元素（指针接收者，可修改 Keys 顺序）
func (v *Value) ArrayUnset(key Value) {
	if v.Kind != KindArray {
		return
	}
	k := key.ToString()
	if _, ok := v.Arr[k]; !ok {
		return
	}
	delete(v.Arr, k)
	for i, kk := range v.Keys {
		if kk == k {
			v.Keys = append(v.Keys[:i], v.Keys[i+1:]...)
			break
		}
	}
}

// ArrayKeysSorted 返回排序后的 key（用于调试/遍历）
func (v Value) ArrayKeysSorted() []string {
	if v.Kind != KindArray {
		return nil
	}
	ks := append([]string{}, v.Keys...)
	sort.Strings(ks)
	return ks
}
