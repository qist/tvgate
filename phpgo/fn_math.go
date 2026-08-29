package phpgo

import (
	"fmt"
	"math"
	"strings"
)

func init() {
	builtins["abs"] = func(e *Env, a []Value) (Value, error) {
		n := a[0].ToInt()
		if n < 0 {
			n = -n
		}
		return NewInt(n), nil
	}
	builtins["ceil"] = func(e *Env, a []Value) (Value, error) {
		return NewFloat(math.Ceil(a[0].ToFloat())), nil
	}
	builtins["floor"] = func(e *Env, a []Value) (Value, error) {
		return NewFloat(math.Floor(a[0].ToFloat())), nil
	}
	builtins["round"] = func(e *Env, a []Value) (Value, error) {
		f := a[0].ToFloat()
		precision := 0
		if len(a) >= 2 {
			precision = int(a[1].ToInt())
		}
		pow := math.Pow(10, float64(precision))
		return NewFloat(math.Round(f*pow) / pow), nil
	}
	builtins["min"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewBool(false), nil
		}
		// 如果第一个参数是数组
		var vals []Value
		if len(a) == 1 && a[0].Kind == KindArray {
			for _, k := range a[0].Keys {
				vals = append(vals, a[0].Arr[k])
			}
		} else {
			vals = a
		}
		if len(vals) == 0 {
			return NewBool(false), nil
		}
		min := vals[0]
		for _, v := range vals[1:] {
			if v.ToInt() < min.ToInt() {
				min = v
			}
		}
		return min, nil
	}
	builtins["max"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewBool(false), nil
		}
		var vals []Value
		if len(a) == 1 && a[0].Kind == KindArray {
			for _, k := range a[0].Keys {
				vals = append(vals, a[0].Arr[k])
			}
		} else {
			vals = a
		}
		if len(vals) == 0 {
			return NewBool(false), nil
		}
		mx := vals[0]
		for _, v := range vals[1:] {
			if v.ToInt() > mx.ToInt() {
				mx = v
			}
		}
		return mx, nil
	}
	builtins["number_format"] = func(e *Env, a []Value) (Value, error) {
		f := a[0].ToFloat()
		dec := 0
		if len(a) >= 2 {
			dec = int(a[1].ToInt())
		}
		// PHP: number_format($num, $decimals, $decimal_point='.', $thousands_sep=',')
		decimalPoint := "."
		if len(a) >= 3 {
			decimalPoint = a[2].ToString()
		}
		thousands := ","
		if len(a) >= 4 {
			thousands = a[3].ToString()
		}
		return NewString(formatFloat(f, dec, thousands, decimalPoint)), nil
	}
	builtins["intdiv"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 || a[1].ToInt() == 0 {
			return NewInt(0), nil
		}
		return NewInt(a[0].ToInt() / a[1].ToInt()), nil
	}
	builtins["pow"] = func(e *Env, a []Value) (Value, error) {
		return NewFloat(math.Pow(a[0].ToFloat(), a[1].ToFloat())), nil
	}
	builtins["sqrt"] = func(e *Env, a []Value) (Value, error) {
		return NewFloat(math.Sqrt(a[0].ToFloat())), nil
	}
	builtins["pi"] = func(e *Env, a []Value) (Value, error) {
		return NewFloat(math.Pi), nil
	}
}

func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func formatFloat(f float64, dec int, thousands, decimalPoint string) string {
	// 用 Go 的 fmt 格式化（四舍五入到指定小数位）
	format := "%." + intToStr(dec) + "f"
	s := fmt.Sprintf(format, f)
	if thousands == "" {
		thousands = ","
	}
	// 分离整数部分与小数部分
	intPart, fracPart := s, ""
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		intPart, fracPart = s[:dot], s[dot:]
		// 替换小数点为自定义 decimalPoint
		if decimalPoint != "" && decimalPoint != "." {
			fracPart = decimalPoint + fracPart[1:]
		}
	}
	neg := ""
	if strings.HasPrefix(intPart, "-") {
		neg, intPart = "-", intPart[1:]
	}
	// 整数部分从右往左每 3 位插入千分位
	var rev []byte
	cnt := 0
	for i := len(intPart) - 1; i >= 0; i-- {
		rev = append(rev, intPart[i])
		cnt++
		if cnt == 3 && i > 0 {
			rev = append(rev, []byte(thousands)...)
			cnt = 0
		}
	}
	var out []byte
	for i := len(rev) - 1; i >= 0; i-- {
		out = append(out, rev[i])
	}
	return neg + string(out) + fracPart
}
