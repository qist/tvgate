package phpgo

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// isNumericKey 判断数组 key 是否为纯数字（PHP 索引数组语义）
func isNumericKey(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// sortStringsByLen 按 key 长度降序排（用于 strtr 等）
func sortStringsByLen(s []string) {
	sort.Slice(s, func(i, j int) bool {
		return len(s[i]) > len(s[j])
	})
}

func parsePort(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// phpSprintf 实现 PHP sprintf 格式化（简化版，用 Go fmt.Sprintf 代理）
func phpSprintf(format string, args []interface{}) string {
	var b strings.Builder
	argIdx := 0
	for i := 0; i < len(format); i++ {
		if format[i] != '%' || i+1 >= len(format) {
			b.WriteByte(format[i])
			continue
		}
		i++
		if format[i] == '%' {
			b.WriteByte('%')
			continue
		}
		// 收集格式说明符： %[flags][width][.precision]specifier
		start := i - 1 // 包含 %
		for i < len(format) && !isPrintfSpec(format[i]) {
			i++
		}
		if i >= len(format) {
			b.WriteString(format[start:])
			break
		}
		fmtStr := format[start : i+1]
		if argIdx < len(args) {
			b.WriteString(goSprintf(fmtStr, args[argIdx]))
			argIdx++
		} else {
			b.WriteString(fmtStr)
		}
	}
	return b.String()
}

func isPrintfSpec(c byte) bool {
	switch c {
	case 'd', 's', 'f', 'x', 'X', 'o', 'b', 'c', 'e', 'g', 'u':
		return true
	}
	return false
}

// goSprintf 用 Go 的 fmt.Sprintf 代理，但对 PHP 特有的做转换
func goSprintf(spec string, arg interface{}) string {
	if len(spec) > 0 && spec[len(spec)-1] == 'b' {
		n := toInt64(arg)
		return strconv.FormatInt(n, 2)
	}
	return fmt.Sprintf(spec, arg)
}

// toInt64 尝试把 interface{} 转为 int64
func toInt64(v interface{}) int64 {
	switch t := v.(type) {
	case int:
		return int64(t)
	case int64:
		return t
	case float64:
		return int64(t)
	case bool:
		if t {
			return 1
		}
		return 0
	case string:
		n, _ := strconv.ParseInt(t, 10, 64)
		return n
	}
	return 0
}

// phpPrintR 递归打印数组（print_r 语义）
func phpPrintR(v Value, depth int) string {
	indent := strings.Repeat("    ", depth)
	switch v.Kind {
	case KindArray:
		var b strings.Builder
		b.WriteString("Array\n")
		b.WriteString(indent + "(\n")
		for _, k := range v.Keys {
			b.WriteString(indent + "    [" + k + "] => ")
			b.WriteString(phpPrintR(v.Arr[k], depth+2))
			b.WriteString("\n")
		}
		b.WriteString(indent + ")\n")
		return b.String()
	case KindNull:
		return ""
	case KindBool:
		if v.Bool {
			return "1"
		}
		return ""
	default:
		return v.ToString()
	}
}

// phpVarDump 输出 var_dump 格式
func phpVarDump(v Value) string {
	switch v.Kind {
	case KindNull:
		return "NULL\n"
	case KindBool:
		if v.Bool {
			return "bool(true)\n"
		}
		return "bool(false)\n"
	case KindInt:
		return fmt.Sprintf("int(%d)\n", v.Int)
	case KindFloat:
		return fmt.Sprintf("float(%v)\n", v.Float)
	case KindString:
		return fmt.Sprintf("string(%d) %q\n", len(v.Str), v.Str)
	case KindArray:
		var b strings.Builder
		b.WriteString(fmt.Sprintf("array(%d) {\n", len(v.Keys)))
		for _, k := range v.Keys {
			b.WriteString(fmt.Sprintf("  [%q]=>\n", k))
			b.WriteString("  " + phpVarDump(v.Arr[k]))
		}
		b.WriteString("}\n")
		return b.String()
	}
	return ""
}

// mathRandIntn 返回 [0, n) 的随机整数
func mathRandIntn(n int) int {
	if n <= 0 {
		return 0
	}
	return cryptoRandIntn(n)
}

// mathRandPerm 返回 [0, n) 的随机排列
func mathRandPerm(n int) []int {
	result := make([]int, n)
	for i := 0; i < n; i++ {
		result[i] = i
	}
	for i := n - 1; i > 0; i-- {
		j := cryptoRandIntn(i + 1)
		result[i], result[j] = result[j], result[i]
	}
	return result
}
