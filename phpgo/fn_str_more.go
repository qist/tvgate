package phpgo

// 字符串扩展（第二批）：strspn / strcspn / substr_compare / strpbrk / levenshtein /
// similar_text / quoted_printable_encode / quoted_printable_decode /
// convert_uuencode / convert_uudecode。

import (
	"fmt"
	"strings"
)

func init() {
	// strspn($str, $mask[, $start[, $length]])：字符串开头在 mask 中的连续字符数
	builtins["strspn"] = func(e *Env, a []Value) (Value, error) {
		return phpStrspnImpl(e, a, true)
	}
	builtins["strcspn"] = func(e *Env, a []Value) (Value, error) {
		return phpStrspnImpl(e, a, false)
	}
	// substr_compare($main, $str, $offset[, $length[, $case_insensitive]])
	builtins["substr_compare"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 3 {
			return NewBool(false), nil
		}
		main := a[0].ToString()
		sub := a[1].ToString()
		offset := int(a[2].ToInt())
		if offset < 0 {
			offset = len(main) + offset
		}
		if offset < 0 || offset > len(main) {
			return NewBool(false), nil
		}
		part := main[offset:]
		if len(a) >= 4 {
			l := a[3].ToInt()
			if l == 0 && a[3].Kind != KindInt {
				// 空值按全长
			} else if l >= 0 && int(l) < len(part) {
				part = part[:l]
			}
		}
		ci := false
		if len(a) >= 5 {
			ci = a[4].ToBool()
		}
		if ci {
			return NewInt(int64(strings.Compare(strings.ToLower(part), strings.ToLower(sub)))), nil
		}
		return NewInt(int64(strings.Compare(part, sub))), nil
	}
	// strpbrk($haystack, $char_list)：返回从最早命中字符开始的后缀；未命中返回 false
	builtins["strpbrk"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		hay := a[0].ToString()
		list := a[1].ToString()
		for i := 0; i < len(hay); i++ {
			if strings.IndexByte(list, hay[i]) >= 0 {
				return NewString(hay[i:]), nil
			}
		}
		return NewBool(false), nil
	}
	// levenshtein($a, $b[, $ins[, $rep[, $del]]])：编辑距离；> 255 返回 -1
	builtins["levenshtein"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		s1, s2 := a[0].ToString(), a[1].ToString()
		ci, cr, cd := 1, 1, 1
		if len(a) >= 5 {
			ci = int(a[2].ToInt())
			cr = int(a[3].ToInt())
			cd = int(a[4].ToInt())
		} else if len(a) >= 4 {
			ci = int(a[2].ToInt())
			cr = int(a[3].ToInt())
		} else if len(a) >= 3 {
			ci = int(a[2].ToInt())
			cr = ci
		}
		l1, l2 := len(s1), len(s2)
		prev := make([]int, l2+1)
		for j := 0; j <= l2; j++ {
			prev[j] = j * cd
		}
		for i := 1; i <= l1; i++ {
			cur := make([]int, l2+1)
			cur[0] = i * ci
			for j := 1; j <= l2; j++ {
				cost := 0
				if s1[i-1] != s2[j-1] {
					cost = cr
				}
				v := prev[j-1] + cost
				if t := prev[j] + ci; t < v {
					v = t
				}
				if t := cur[j-1] + cd; t < v {
					v = t
				}
				cur[j] = v
			}
			prev = cur
		}
		if prev[l2] > 255 {
			return NewInt(-1), nil
		}
		return NewInt(int64(prev[l2])), nil
	}
	// similar_text($s1, $s2[, &$percent])
	builtins["similar_text"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewInt(0), nil
		}
		s1, s2 := a[0].ToString(), a[1].ToString()
		sim := phpSimilarText(s1, s2)
		if len(a) >= 3 {
			pct := NewFloat(float64(sim) * 200.0 / float64(len(s1)+len(s2)))
			writeRef(e, a[2], pct)
		}
		return NewInt(int64(sim)), nil
	}
	builtins["quoted_printable_encode"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewString(""), nil
		}
		return NewString(phpQPEncode(a[0].ToString())), nil
	}
	builtins["quoted_printable_decode"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewString(""), nil
		}
		return NewString(phpQPDecode(a[0].ToString())), nil
	}
	builtins["convert_uuencode"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewString(""), nil
		}
		return NewString(phpUUEncode(a[0].ToString())), nil
	}
	builtins["convert_uudecode"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewString(""), nil
		}
		out, ok := phpUUDecode(a[0].ToString())
		if !ok {
			return NewBool(false), nil
		}
		return NewString(out), nil
	}
}

func phpStrspnImpl(e *Env, a []Value, span bool) (Value, error) {
	if len(a) < 2 {
		return NewInt(0), nil
	}
	s := a[0].ToString()
	mask := a[1].ToString()
	start := 0
	if len(a) >= 3 {
		start = int(a[2].ToInt())
		if start < 0 {
			start = len(s) + start
		}
	}
	if start < 0 || start > len(s) {
		return NewInt(0), nil
	}
	end := len(s)
	if len(a) >= 4 {
		l := int(a[3].ToInt())
		if l >= 0 && start+l < end {
			end = start + l
		}
	}
	count := 0
	for i := start; i < end; i++ {
		in := strings.IndexByte(mask, s[i]) >= 0
		if span && !in {
			break
		}
		if !span && in {
			break
		}
		count++
	}
	return NewInt(int64(count)), nil
}

// phpSimilarText 求两字符串相似度（PHP similar_text 的递归算法）。
func phpSimilarText(a, b string) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	// 找最长公共连续子串
	max := 0
	pa, pb := 0, 0
	for i := 0; i < len(a); i++ {
		for j := 0; j < len(b); j++ {
			k := 0
			for i+k < len(a) && j+k < len(b) && a[i+k] == b[j+k] {
				k++
			}
			if k > max {
				max = k
				pa, pb = i, j
			}
		}
	}
	if max == 0 {
		return 0
	}
	sum := max
	if pa > 0 && pb > 0 {
		sum += phpSimilarText(a[:pa], b[:pb])
	}
	if pa+max < len(a) && pb+max < len(b) {
		sum += phpSimilarText(a[pa+max:], b[pb+max:])
	}
	return sum
}

// phpQPEncode quoted-printable 编码（RFC 2045，软换行 76 列）
func phpQPEncode(s string) string {
	var b strings.Builder
	col := 0
	data := []byte(s)
	for i := 0; i < len(data); i++ {
		c := data[i]
		var enc string
		if c == '=' {
			enc = "=3D"
		} else if c == ' ' || c == '\t' {
			// 行尾空白需要编码
			if i+1 >= len(data) || data[i+1] == '\n' || data[i+1] == '\r' {
				if c == ' ' {
					enc = "=20"
				} else {
					enc = "=09"
				}
			} else {
				enc = string(c)
			}
		} else if c == '\n' {
			b.WriteByte('\n')
			col = 0
			continue
		} else if c == '\r' {
			continue
		} else if c >= 33 && c <= 126 {
			enc = string(c)
		} else {
			enc = fmt.Sprintf("=%02X", c)
		}
		// 软换行：超过 75 列插入 "=\n"
		if col+len(enc) > 75 {
			b.WriteString("=\n")
			col = 0
		}
		b.WriteString(enc)
		col += len(enc)
	}
	return b.String()
}

// phpQPDecode quoted-printable 解码
func phpQPDecode(s string) string {
	var b strings.Builder
	data := []byte(s)
	for i := 0; i < len(data); i++ {
		c := data[i]
		switch {
		case c == '=':
			if i+1 < len(data) && data[i+1] == '\n' {
				i++ // 软换行
				continue
			}
			if i+2 < len(data) && data[i+1] == '\r' && data[i+2] == '\n' {
				i += 2
				continue
			}
			if i+2 < len(data) {
				hi, ok1 := hexNibble(data[i+1])
				lo, ok2 := hexNibble(data[i+2])
				if ok1 && ok2 {
					b.WriteByte(hi<<4 | lo)
					i += 2
					continue
				}
			}
			b.WriteByte('=')
		case c == '\r':
			continue
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func hexNibble(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// phpUUEncode uuencode（每行 45 字节）
func phpUUEncode(s string) string {
	data := []byte(s)
	var b strings.Builder
	for start := 0; start < len(data); start += 45 {
		n := len(data) - start
		if n > 45 {
			n = 45
		}
		b.WriteByte(byte(n) + 32)
		// 每 3 字节一组输出 4 字符
		for i := start; i < start+n; i += 3 {
			c1 := data[i]
			c2, c3 := byte(0), byte(0)
			has2, has3 := false, false
			if i+1 < start+n {
				c2 = data[i+1]
				has2 = true
			}
			if i+2 < start+n {
				c3 = data[i+2]
				has3 = true
			}
			b.WriteByte(c1>>2 + 32)
			b.WriteByte((c1&3)<<4|c2>>4 + 32)
			if has2 {
				b.WriteByte((c2&15)<<2|c3>>6 + 32)
			} else {
				b.WriteByte('`')
			}
			if has3 {
				b.WriteByte(c3&63 + 32)
			} else {
				b.WriteByte('`')
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// phpUUDecode uudecode（兼容含/不含末尾 '`' 终止行）
func phpUUDecode(s string) (string, bool) {
	var out []byte
	lines := strings.Split(s, "\n")
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		// 去掉可能的前导长度字符行中多余空格
		if line[0] == '`' {
			break
		}
		if line[0] < 32 || line[0] > 126 {
			continue
		}
		n := int(line[0]) - 32
		if n < 0 {
			n = 0
		}
		body := []byte(line[1:])
		got := 0
		for i := 0; i+3 < len(body) && got < n; i += 4 {
			val := func(c byte) byte {
				if c == '`' {
					return 0
				}
				return c - 32
			}
			c1, c2, c3, c4 := val(body[i]), val(body[i+1]), val(body[i+2]), val(body[i+3])
			out = append(out, c1<<2|c2>>4)
			got++
			if got < n {
				out = append(out, c2<<4|c3>>2)
				got++
			}
			if got < n {
				out = append(out, c3<<6|c4)
				got++
			}
		}
	}
	return string(out), true
}
