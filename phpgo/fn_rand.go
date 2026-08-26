package phpgo

import (
	"crypto/rand"
	"math/big"
	"strconv"
	"time"
)

// cryptoRandIntn 返回 [0, n) 的加密安全随机整数
func cryptoRandIntn(n int) int {
	if n <= 0 {
		return 0
	}
	max := big.NewInt(int64(n))
	nBig, err := rand.Int(rand.Reader, max)
	if err != nil {
		return 0
	}
	return int(nBig.Int64())
}

// cryptoRandBytes 返回 n 个随机字节
func cryptoRandBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}

func init() {
	// rand / mt_rand / random_int
	builtins["rand"] = func(e *Env, a []Value) (Value, error) {
		if len(a) >= 2 {
			lo := a[0].ToInt()
			hi := a[1].ToInt()
			if lo > hi {
				lo, hi = hi, lo
			}
			rangeVal := hi - lo + 1
			return NewInt(lo + int64(cryptoRandIntn(int(rangeVal)))), nil
		}
		return NewInt(int64(cryptoRandIntn(2147483647))), nil
	}

	builtins["mt_rand"] = builtins["rand"]

	builtins["random_int"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewInt(0), nil
		}
		lo := a[0].ToInt()
		hi := a[1].ToInt()
		if lo > hi {
			lo, hi = hi, lo
		}
		return NewInt(lo + int64(cryptoRandIntn(int(hi-lo+1)))), nil
	}

	builtins["mt_getrandmax"] = func(e *Env, a []Value) (Value, error) {
		return NewInt(2147483647), nil
	}

	builtins["uniqid"] = func(e *Env, a []Value) (Value, error) {
		prefix := ""
		if len(a) >= 1 {
			prefix = a[0].ToString()
		}
		now := time.Now().UnixMicro()
		// PHP uniqid 格式: prefix + hex(timestamp_subsec) + hex(random)
		s := prefix + strconv.FormatInt(now, 16) + strconv.FormatInt(int64(cryptoRandIntn(0xFFFF)), 16)
		return NewString(s), nil
	}

	builtins["random_bytes"] = func(e *Env, a []Value) (Value, error) {
		n := int(a[0].ToInt())
		if n < 0 {
			n = 0
		}
		b := cryptoRandBytes(n)
		return NewString(string(b)), nil
	}

	builtins["usleep"] = func(e *Env, a []Value) (Value, error) {
		// 简化：不真正 sleep
		return NewNull(), nil
	}
}
