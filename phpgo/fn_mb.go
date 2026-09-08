package phpgo

// mbstring 补齐：mb_internal_encoding / mb_detect_encoding / mb_convert_encoding。
// 依赖 golang.org/x/text（GBK/GB18030/Big5/ISO-8859-1/UTF-16 等）。
//
// 说明：mb_strlen/mb_substr/mb_strpos 等仍为 str* 字节语义别名（fn_str_case.go 注册），
// 此处不再重复注册以避免 init 顺序不确定性；如需按字符语义可后续整体替换。

import (
	"bytes"
	"strings"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// mbNormalizeCharset 规范化字符集名（小写、去 -_ 空格）
func mbNormalizeCharset(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, " ", "")
	return s
}

// mbDecoder 返回 from 编码的解码器（字节 → UTF-8）
func mbDecoder(from string) (encoding.Encoding, bool) {
	switch mbNormalizeCharset(from) {
	case "utf8", "utf", "utf8mb4":
		return nil, true // 恒等
	case "ascii", "usascii", "7bit":
		return nil, true // ASCII 是 UTF-8 子集，按恒等处理
	case "latin1", "iso88591", "cp1252", "windows1252", "latin", "l1":
		return charmap.ISO8859_1, true
	case "gbk", "cp936", "ms936", "gb2312", "euc cn", "gb18030", "hz gb2312":
		return simplifiedchinese.GB18030, true // GB18030 是 GBK/GB2312 超集
	case "big5", "big 5", "cp950", "big5hkscs":
		return traditionalchinese.Big5, true
	case "utf16", "utf16le", "ucs2", "ucs2le":
		return unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM), true
	case "utf16be", "ucs2be":
		return unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM), true
	}
	return nil, false
}

// mbEncodeTo 把 UTF-8 字符串转成目标编码的字节串
func mbEncodeTo(s string, to string) ([]byte, bool) {
	switch mbNormalizeCharset(to) {
	case "utf8", "utf", "utf8mb4":
		return []byte(s), true
	case "ascii", "usascii":
		return []byte(s), true
	case "htmlentities", "htmlentitydecode", "html":
		return []byte(phpHTMLEncode(s)), true
	case "latin1", "iso88591", "cp1252", "windows1252", "latin", "l1":
		enc := charmap.ISO8859_1.NewEncoder()
		out, _, err := transform.Bytes(enc, []byte(s))
		return out, err == nil
	case "gbk", "cp936", "ms936", "gb2312", "euc cn", "gb18030", "hz gb2312":
		enc := simplifiedchinese.GB18030.NewEncoder()
		out, _, err := transform.Bytes(enc, []byte(s))
		return out, err == nil
	case "big5", "big 5", "cp950", "big5hkscs":
		enc := traditionalchinese.Big5.NewEncoder()
		out, _, err := transform.Bytes(enc, []byte(s))
		return out, err == nil
	case "utf16", "utf16le", "ucs2", "ucs2le":
		enc := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewEncoder()
		out, _, err := transform.Bytes(enc, []byte(s))
		return out, err == nil
	case "utf16be", "ucs2be":
		enc := unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM).NewEncoder()
		out, _, err := transform.Bytes(enc, []byte(s))
		return out, err == nil
	}
	return nil, false
}

// mbDecodeFrom 把指定编码的字节转成 UTF-8 字符串
func mbDecodeFrom(b []byte, from string) (string, bool) {
	dec, isIdentity := mbDecoder(from)
	if isIdentity {
		return string(b), true
	}
	if dec == nil {
		return "", false
	}
	out, _, err := transform.Bytes(dec.NewDecoder(), b)
	if err != nil {
		return "", false
	}
	return string(out), true
}

// mbIsValidUTF8 判断是否合法 UTF-8
func mbIsValidUTF8(s []byte) bool {
	return bytes.ToValidUTF8(s, []byte("\ufffd")) != nil && !bytes.Contains(s, []byte("\ufffd")) &&
		validateUTF8(s)
}

// validateUTF8 用 strings 判非法字节（ToValidUTF8 等价检查）
func validateUTF8(s []byte) bool {
	return len(bytes.ToValidUTF8(s, []byte(""))) == len(s)
}

// mbIsASCII 判断纯 ASCII
func mbIsASCII(s []byte) bool {
	for _, c := range s {
		if c >= 0x80 {
			return false
		}
	}
	return true
}

func init() {
	builtins["mb_internal_encoding"] = func(e *Env, a []Value) (Value, error) {
		if len(a) >= 1 {
			enc := a[0].ToString()
			if _, ok := mbDecoder(enc); ok || mbNormalizeCharset(enc) == "auto" {
				e.mbInternalEnc = enc
				return NewBool(true), nil
			}
			return NewBool(false), nil
		}
		if e.mbInternalEnc == "" {
			return NewString("UTF-8"), nil
		}
		return NewString(e.mbInternalEnc), nil
	}
	// mb_detect_encoding($str[, $encodings = null[, $strict = false]])
	builtins["mb_detect_encoding"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		data := []byte(a[0].ToString())
		strict := false
		if len(a) >= 3 {
			strict = a[2].ToBool()
		}
		var candidates []string
		if len(a) >= 2 {
			v := a[1]
			if v.Kind == KindArray {
				for _, k := range v.Keys {
					candidates = append(candidates, v.Arr[k].ToString())
				}
			} else if v.Kind == KindString && v.Str != "" {
				for _, s := range strings.Split(v.Str, ",") {
					if t := strings.TrimSpace(s); t != "" {
						candidates = append(candidates, t)
					}
				}
			}
		}
		if len(candidates) == 0 {
			candidates = []string{"ASCII", "UTF-8"}
		}
		for _, c := range candidates {
			switch mbNormalizeCharset(c) {
			case "ascii", "usascii":
				if mbIsASCII(data) {
					return NewString(c), nil
				}
			case "utf8", "utf":
				if validateUTF8(data) && (strict || !mbIsASCII(data)) {
					return NewString(c), nil
				}
				if !strict && mbIsASCII(data) {
					continue // ASCII 已优先匹配
				}
			default:
				out, ok := mbDecodeFrom(data, c)
				// 转码结果不应出现替换符，否则视为探测失败
				if ok && !strings.ContainsRune(out, '\uFFFD') {
					return NewString(c), nil
				}
			}
		}
		return NewBool(false), nil
	}
	// mb_convert_encoding($str, $to_encoding[, $from_encoding = mb_internal_encoding])
	builtins["mb_convert_encoding"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		s := a[0].ToString()
		to := a[1].ToString()
		from := e.mbInternalEnc
		if from == "" {
			from = "UTF-8"
		}
		if len(a) >= 3 {
			fv := a[2].ToString()
			if mbNormalizeCharset(fv) != "auto" && mbNormalizeCharset(fv) != "pass" {
				from = fv
			}
		}
		if mbNormalizeCharset(from) == "auto" {
			// 自动探测输入编码
			if validateUTF8([]byte(s)) {
				from = "UTF-8"
			} else if mbIsASCII([]byte(s)) {
				from = "ASCII"
			} else {
				from = "GBK"
			}
		}
		utf8str, ok := mbDecodeFrom([]byte(s), from)
		if !ok {
			return NewBool(false), nil
		}
		if mbNormalizeCharset(to) == "auto" {
			to = "UTF-8"
		}
		out, ok := mbEncodeTo(utf8str, to)
		if !ok {
			return NewBool(false), nil
		}
		return NewString(string(out)), nil
	}
}

// phpHTMLEncode 简单 HTML 实体编码（mb_convert_encoding 目标为 HTML-ENTITIES 时用）
func phpHTMLEncode(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
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
			b.WriteRune(r)
		}
	}
	return b.String()
}
