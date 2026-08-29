package phpgo

import (
	"strconv"
	"strings"
)

// encodeHTMLEntity 把字节按 HTML 实体编码：
// & < > " ' 用命名实体，其余 >127 的字节用 &#N;（Latin-1 风格，接近 PHP 默认行为）
func encodeHTMLEntity(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&#039;")
		default:
			if c > 127 {
				b.WriteString("&#" + strconv.Itoa(int(c)) + ";")
			} else {
				b.WriteByte(c)
			}
		}
	}
	return b.String()
}

// decodeHTMLEntity 解码常见命名实体与数字实体（十进制/十六进制）
func decodeHTMLEntity(s string) string {
	replacer := strings.NewReplacer(
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", "\"",
		"&apos;", "'",
		"&#039;", "'",
		"&#39;", "'",
		"&nbsp;", "\u00a0",
		"&copy;", "\u00a9",
		"&reg;", "\u00ae",
		"&euro;", "\u20ac",
		"&pound;", "\u00a3",
		"&yen;", "\u00a5",
	)
	s = replacer.Replace(s)
	// 数字实体 &#123; 和 &#x1F;
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '&' && i+2 < len(s) && s[i+1] == '#' {
			j := i + 2
			hex := false
			if j < len(s) && (s[j] == 'x' || s[j] == 'X') {
				hex = true
				j++
			}
			start := j
			for j < len(s) && s[j] != ';' {
				j++
			}
			if j < len(s) && j > start {
				body := s[start:j]
				var n int64
				if hex {
					n, _ = strconv.ParseInt(body, 16, 32)
				} else {
					n, _ = strconv.ParseInt(body, 10, 32)
				}
				if n >= 0 && n <= 0x10FFFF {
					out.WriteRune(rune(n))
					i = j
					continue
				}
			}
		}
		out.WriteByte(s[i])
	}
	return out.String()
}

// utf8Decode UTF-8 → ISO-8859-1（有损；无效/超出 Latin-1 的码点替换为 '?'）
func utf8Decode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := decodeRuneUTF8(s[i:])
		if r >= 0 && r <= 255 {
			b.WriteByte(byte(r))
		} else {
			b.WriteByte('?')
		}
		i += size
	}
	return b.String()
}

// decodeRuneUTF8 手写解码单个 UTF-8 序列，返回码点与字节长度
func decodeRuneUTF8(s string) (rune, int) {
	if len(s) == 0 {
		return 0, 1
	}
	c := s[0]
	switch {
	case c < 0x80:
		return rune(c), 1
	case c&0xE0 == 0xC0 && len(s) >= 2:
		return rune(c&0x1F)<<6 | rune(s[1]&0x3F), 2
	case c&0xF0 == 0xE0 && len(s) >= 3:
		return rune(c&0x0F)<<12 | rune(s[1]&0x3F)<<6 | rune(s[2]&0x3F), 3
	case c&0xF8 == 0xF0 && len(s) >= 4:
		return rune(c&0x07)<<18 | rune(s[1]&0x3F)<<12 | rune(s[2]&0x3F)<<6 | rune(s[3]&0x3F), 4
	}
	return 0xFFFD, 1
}

func init() {
	builtins["htmlentities"] = func(e *Env, a []Value) (Value, error) {
		return NewString(encodeHTMLEntity(a[0].ToString())), nil
	}
	builtins["html_entity_decode"] = func(e *Env, a []Value) (Value, error) {
		return NewString(decodeHTMLEntity(a[0].ToString())), nil
	}
	builtins["utf8_decode"] = func(e *Env, a []Value) (Value, error) {
		return NewString(utf8Decode(a[0].ToString())), nil
	}
}
