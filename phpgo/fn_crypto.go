package phpgo

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"strconv"
	"strings"
)

func init() {
	builtins["md5"] = func(e *Env, a []Value) (Value, error) {
		s := a[0].ToString()
		raw := false
		if len(a) >= 2 {
			raw = a[1].ToBool()
		}
		h := md5.Sum([]byte(s))
		if raw {
			return NewString(string(h[:])), nil
		}
		return NewString(hex.EncodeToString(h[:])), nil
	}
	builtins["sha1"] = func(e *Env, a []Value) (Value, error) {
		s := a[0].ToString()
		raw := false
		if len(a) >= 2 {
			raw = a[1].ToBool()
		}
		h := sha1.Sum([]byte(s))
		if raw {
			return NewString(string(h[:])), nil
		}
		return NewString(hex.EncodeToString(h[:])), nil
	}
	builtins["hash"] = func(e *Env, a []Value) (Value, error) {
		algo := a[0].ToString()
		s := a[1].ToString()
		raw := false
		if len(a) >= 3 {
			raw = a[2].ToBool()
		}
		switch strings.ToLower(algo) {
		case "md5":
			h := md5.Sum([]byte(s))
			if raw {
				return NewString(string(h[:])), nil
			}
			return NewString(hex.EncodeToString(h[:])), nil
		case "sha1":
			h := sha1.Sum([]byte(s))
			if raw {
				return NewString(string(h[:])), nil
			}
			return NewString(hex.EncodeToString(h[:])), nil
		case "sha256":
			h := sha256.Sum256([]byte(s))
			if raw {
				return NewString(string(h[:])), nil
			}
			return NewString(hex.EncodeToString(h[:])), nil
		}
		return NewString(""), nil
	}
	builtins["hash_hmac"] = func(e *Env, a []Value) (Value, error) {
		algo := a[0].ToString()
		data := a[1].ToString()
		key := a[2].ToString()
		raw := false
		if len(a) >= 4 {
			raw = a[3].ToBool()
		}
		var mac hash.Hash
		switch strings.ToLower(algo) {
		case "md5":
			mac = hmac.New(md5.New, []byte(key))
		case "sha1":
			mac = hmac.New(sha1.New, []byte(key))
		case "sha256":
			mac = hmac.New(sha256.New, []byte(key))
		default:
			return NewString(""), nil
		}
		mac.Write([]byte(data))
		if raw {
			return NewString(string(mac.Sum(nil))), nil
		}
		return NewString(hex.EncodeToString(mac.Sum(nil))), nil
	}
	builtins["crc32"] = func(e *Env, a []Value) (Value, error) {
		s := []byte(a[0].ToString())
		var crc uint32 = 0xFFFFFFFF
		for _, b := range s {
			crc ^= uint32(b)
			for i := 0; i < 8; i++ {
				if crc&1 != 0 {
					crc = (crc >> 1) ^ 0xEDB88320
				} else {
					crc >>= 1
				}
			}
		}
		return NewInt(int64(crc ^ 0xFFFFFFFF)), nil
	}
	builtins["base64_encode"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewString(""), nil
		}
		return NewString(base64.StdEncoding.EncodeToString([]byte(a[0].ToString()))), nil
	}
	builtins["base64_decode"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewString(""), nil
		}
		b, err := base64.StdEncoding.DecodeString(a[0].ToString())
		if err != nil {
			return NewString(""), nil
		}
		return NewString(string(b)), nil
	}
	builtins["dechex"] = func(e *Env, a []Value) (Value, error) {
		return NewString(strconv.FormatInt(a[0].ToInt(), 16)), nil
	}
	builtins["hexdec"] = func(e *Env, a []Value) (Value, error) {
		n, _ := strconv.ParseInt(a[0].ToString(), 16, 64)
		return NewInt(n), nil
	}
	builtins["decbin"] = func(e *Env, a []Value) (Value, error) {
		return NewString(strconv.FormatInt(a[0].ToInt(), 2)), nil
	}
	builtins["bindec"] = func(e *Env, a []Value) (Value, error) {
		n, _ := strconv.ParseInt(a[0].ToString(), 2, 64)
		return NewInt(n), nil
	}
	builtins["base_convert"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 3 {
			return NewString("0"), nil
		}
		n, _ := strconv.ParseInt(a[0].ToString(), int(a[1].ToInt()), 64)
		return NewString(strconv.FormatInt(n, int(a[2].ToInt()))), nil
	}
	builtins["chr"] = func(e *Env, a []Value) (Value, error) {
		return NewString(string(rune(a[0].ToInt()))), nil
	}
	builtins["ord"] = func(e *Env, a []Value) (Value, error) {
		s := a[0].ToString()
		if len(s) == 0 {
			return NewInt(0), nil
		}
		return NewInt(int64(s[0])), nil
	}
	builtins["pack"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewString(""), nil
		}
		format := a[0].ToString()
		var b []byte
		argIdx := 1
		for i := 0; i < len(format); i++ {
			switch format[i] {
			case 'C':
				if argIdx < len(a) {
					b = append(b, byte(a[argIdx].ToInt()))
					argIdx++
				}
			case 'n':
				if argIdx < len(a) {
					v := a[argIdx].ToInt()
					buf := make([]byte, 2)
					binary.BigEndian.PutUint16(buf, uint16(v))
					b = append(b, buf...)
					argIdx++
				}
			case 'N':
				if argIdx < len(a) {
					v := a[argIdx].ToInt()
					buf := make([]byte, 4)
					binary.BigEndian.PutUint32(buf, uint32(v))
					b = append(b, buf...)
					argIdx++
				}
			case 'v':
				if argIdx < len(a) {
					v := a[argIdx].ToInt()
					buf := make([]byte, 2)
					binary.LittleEndian.PutUint16(buf, uint16(v))
					b = append(b, buf...)
					argIdx++
				}
			case 'V':
				if argIdx < len(a) {
					v := a[argIdx].ToInt()
					buf := make([]byte, 4)
					binary.LittleEndian.PutUint32(buf, uint32(v))
					b = append(b, buf...)
					argIdx++
				}
			case 'a', 'A':
				// NUL/space padded string
				if argIdx < len(a) {
					s := a[argIdx].ToString()
					b = append(b, []byte(s)...)
					pad := byte(0)
					if format[i] == 'A' {
						pad = ' '
					}
					// 查看是否有数量后缀
					// 简化：直接追加
					_ = pad
					argIdx++
				}
			case '*':
				// repeat count
			}
		}
		return NewString(string(b)), nil
	}
	builtins["unpack"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewArray(), nil
		}
		format := a[0].ToString()
		data := []byte(a[1].ToString())
		result := NewArray()
		argIdx := 0
		keyIdx := 1
		for i := 0; i < len(format); i++ {
			switch format[i] {
			case 'C':
				if argIdx < len(data) {
					result.ArraySet(NewInt(int64(keyIdx)), NewInt(int64(data[argIdx])))
					argIdx++
					keyIdx++
				}
			case 'n':
				if argIdx+1 < len(data) {
					v := binary.BigEndian.Uint16(data[argIdx:])
					result.ArraySet(NewInt(int64(keyIdx)), NewInt(int64(v)))
					argIdx += 2
					keyIdx++
				}
			case 'N':
				if argIdx+3 < len(data) {
					v := binary.BigEndian.Uint32(data[argIdx:])
					result.ArraySet(NewInt(int64(keyIdx)), NewInt(int64(v)))
					argIdx += 4
					keyIdx++
				}
			case 'v':
				if argIdx+1 < len(data) {
					v := binary.LittleEndian.Uint16(data[argIdx:])
					result.ArraySet(NewInt(int64(keyIdx)), NewInt(int64(v)))
					argIdx += 2
					keyIdx++
				}
			case 'V':
				if argIdx+3 < len(data) {
					v := binary.LittleEndian.Uint32(data[argIdx:])
					result.ArraySet(NewInt(int64(keyIdx)), NewInt(int64(v)))
					argIdx += 4
					keyIdx++
				}
			case '*':
				// 跳过
			}
			// 跳过数字后缀
			for i < len(format) && format[i] >= '0' && format[i] <= '9' {
				i++
			}
		}
		return result, nil
	}
	// md5：返回小写十六进制
	builtins["md5"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewString(""), nil
		}
		sum := md5.Sum([]byte(a[0].ToString()))
		return NewString(hex.EncodeToString(sum[:])), nil
	}
	// sha1：返回小写十六进制
	builtins["sha1"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewString(""), nil
		}
		sum := sha1.Sum([]byte(a[0].ToString()))
		return NewString(hex.EncodeToString(sum[:])), nil
	}
}
