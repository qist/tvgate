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

// phpSprintf 实现 PHP sprintf 格式化（支持 %1$s 位置参数、%02X 等）
func phpSprintf(format string, args []interface{}) string {
	var b strings.Builder
	argIdx := 0
	i := 0
	for i < len(format) {
		if format[i] != '%' || i+1 >= len(format) {
			b.WriteByte(format[i])
			i++
			continue
		}
		// 记录 % 位置
		pctStart := i
		i++ // 跳过 %

		// %% 转义
		if format[i] == '%' {
			b.WriteByte('%')
			i++
			continue
		}

		// 检查 PHP 位置参数： %1$s, %2$d 等
		posArg := -1
		if i < len(format) && format[i] >= '0' && format[i] <= '9' {
			// 先保存位置，尝试解析 位置$ 格式
			savedI := i
			num := 0
			for i < len(format) && format[i] >= '0' && format[i] <= '9' {
				num = num*10 + int(format[i]-'0')
				i++
			}
			if i < len(format) && format[i] == '$' {
				posArg = num // 1-based
				i++          // 跳过 $
			} else {
				// 不是位置参数，回退
				i = savedI
			}
		}

		// 收集格式说明符： [flags][width][.precision]specifier
		for i < len(format) && !isPrintfSpec(format[i]) {
			i++
		}
		if i >= len(format) {
			// 没有找到 spec 字符，原样输出
			b.WriteString(format[pctStart:])
			break
		}

		// 完整的格式串（从 % 到 spec 字符）
		fmtStr := format[pctStart : i+1]
		// 去掉位置参数前缀（如 %1$s → %s）
		goFmt := convertPhpFormat(fmtStr)

		var arg interface{}
		if posArg > 0 && posArg <= len(args) {
			arg = args[posArg-1]
		} else if argIdx < len(args) {
			arg = args[argIdx]
			argIdx++
		} else {
			b.WriteString(fmtStr)
			i++
			continue
		}
		b.WriteString(goSprintf(goFmt, arg))
		i++ // 跳过 spec 字符
	}
	return b.String()
}

// convertPhpFormat 去掉 PHP 位置参数前缀（如 %1$s → %s, %2$d → %d）
func convertPhpFormat(s string) string {
	if len(s) < 2 || s[0] != '%' {
		return s
	}
	// 找到 $ 的位置
	dollarIdx := -1
	for i := 1; i < len(s); i++ {
		if s[i] == '$' {
			dollarIdx = i
			break
		}
		if s[i] < '0' || s[i] > '9' {
			break
		}
	}
	if dollarIdx > 0 {
		return "%" + s[dollarIdx+1:]
	}
	return s
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

// valuesToIface 把 []Value 转为 sprintf 用的 []interface{}
func valuesToIface(vs []Value) []interface{} {
	out := make([]interface{}, 0, len(vs))
	for _, v := range vs {
		switch v.Kind {
		case KindInt:
			out = append(out, v.Int)
		case KindFloat:
			out = append(out, v.Float)
		case KindString:
			out = append(out, v.Str)
		case KindBool:
			out = append(out, v.Bool)
		default:
			out = append(out, v.ToString())
		}
	}
	return out
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
