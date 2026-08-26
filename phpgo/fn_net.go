package phpgo

import (
	"encoding/hex"
	"net/url"
	"strconv"
)

func init() {
	// rawurlencode：RFC3986 编码（空格 -> %20）
	builtins["rawurlencode"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewString(""), nil
		}
		return NewString(url.PathEscape(a[0].ToString())), nil
	}
	// rawurldecode 对应（对称补充）
	builtins["rawurldecode"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewString(""), nil
		}
		s, err := url.PathUnescape(a[0].ToString())
		if err != nil {
			return NewString(a[0].ToString()), nil
		}
		return NewString(s), nil
	}
	// hex2bin：十六进制字符串 -> 二进制
	builtins["hex2bin"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewString(""), nil
		}
		b, err := hex.DecodeString(a[0].ToString())
		if err != nil {
			return NewBool(false), nil
		}
		return NewString(string(b)), nil
	}
	// bin2hex：二进制 -> 十六进制
	builtins["bin2hex"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewString(""), nil
		}
		return NewString(hex.EncodeToString([]byte(a[0].ToString()))), nil
	}
	// ip2long：IPv4 字符串 -> 有符号 32 位整数（PHP 语义）
	builtins["ip2long"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewBool(false), nil
		}
		ip := a[0].ToString()
		var b [4]byte
		var n int
		for i := 0; i < 4; i++ {
			if n >= len(ip) {
				return NewBool(false), nil
			}
			// 段
			start := n
			for n < len(ip) && ip[n] != '.' {
				n++
			}
			seg, err := strconv.Atoi(ip[start:n])
			if err != nil || seg > 255 || seg < 0 {
				return NewBool(false), nil
			}
			b[i] = byte(seg)
			if n < len(ip) {
				n++ // 跳过 '.'
			}
		}
		val := int64(b[0])<<24 | int64(b[1])<<16 | int64(b[2])<<8 | int64(b[3])
		if val > 2147483647 { // 转为负数以匹配 PHP 有符号行为
			val -= 4294967296
		}
		return NewInt(val), nil
	}
	// long2ip：整数 -> IPv4 字符串
	builtins["long2ip"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewString(""), nil
		}
		v := a[0].ToInt()
		v = v & 0xFFFFFFFF
		return NewString(strconv.FormatInt((v>>24)&0xFF, 10) + "." +
			strconv.FormatInt((v>>16)&0xFF, 10) + "." +
			strconv.FormatInt((v>>8)&0xFF, 10) + "." +
			strconv.FormatInt(v&0xFF, 10)), nil
	}
	// utf8_encode：ISO-8859-1 -> UTF-8（PHP 历史语义）
	builtins["utf8_encode"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewString(""), nil
		}
		b := []byte(a[0].ToString())
		out := make([]byte, 0, len(b)*2)
		for _, c := range b {
			if c < 0x80 {
				out = append(out, c)
			} else {
				out = append(out, 0xC2|(c>>6), 0x80|(c&0x3F))
			}
		}
		return NewString(string(out)), nil
	}
}
