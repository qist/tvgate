package phpgo

import (
	"strings"
)

// 截断到前 n 字节（strncmp 语义）
func truncateN(s string, n int) string {
	if n >= len(s) {
		return s
	}
	return s[:n]
}

// parseLeadingNumber 解析字符串开头的十进制整数
func parseLeadingNumber(s string) int64 {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int64(c-'0')
	}
	return n
}

func isDigitByte(c byte) bool { return c >= '0' && c <= '9' }

// naturalCompare 自然顺序比较（strnatcmp 语义）：数字段按数值比较，其余按字节序
func naturalCompare(a, b string) int {
	ia, ib := 0, 0
	for ia < len(a) && ib < len(b) {
		if isDigitByte(a[ia]) && isDigitByte(b[ib]) {
			na := parseLeadingNumber(a[ia:])
			nb := parseLeadingNumber(b[ib:])
			if na < nb {
				return -1
			}
			if na > nb {
				return 1
			}
			// 数值相等，跳过数字段（较长数字串的补零仍视为相等）
			for ia < len(a) && isDigitByte(a[ia]) {
				ia++
			}
			for ib < len(b) && isDigitByte(b[ib]) {
				ib++
			}
			continue
		}
		if a[ia] < b[ib] {
			return -1
		}
		if a[ia] > b[ib] {
			return 1
		}
		ia++
		ib++
	}
	if ia < len(a) {
		return 1
	}
	if ib < len(b) {
		return -1
	}
	return 0
}

func init() {
	builtins["strcmp"] = func(e *Env, a []Value) (Value, error) {
		return NewInt(int64(strings.Compare(a[0].ToString(), a[1].ToString()))), nil
	}
	builtins["strcasecmp"] = func(e *Env, a []Value) (Value, error) {
		return NewInt(int64(strings.Compare(strings.ToLower(a[0].ToString()), strings.ToLower(a[1].ToString())))), nil
	}
	builtins["strncmp"] = func(e *Env, a []Value) (Value, error) {
		n := int(a[2].ToInt())
		if n < 0 {
			n = 0
		}
		return NewInt(int64(strings.Compare(truncateN(a[0].ToString(), n), truncateN(a[1].ToString(), n)))), nil
	}
	builtins["strncasecmp"] = func(e *Env, a []Value) (Value, error) {
		n := int(a[2].ToInt())
		if n < 0 {
			n = 0
		}
		return NewInt(int64(strings.Compare(strings.ToLower(truncateN(a[0].ToString(), n)), strings.ToLower(truncateN(a[1].ToString(), n))))), nil
	}
	builtins["strnatcmp"] = func(e *Env, a []Value) (Value, error) {
		return NewInt(int64(naturalCompare(a[0].ToString(), a[1].ToString()))), nil
	}
	builtins["strnatcasecmp"] = func(e *Env, a []Value) (Value, error) {
		return NewInt(int64(naturalCompare(strings.ToLower(a[0].ToString()), strings.ToLower(a[1].ToString())))), nil
	}
}
