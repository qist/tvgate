package phpgo

import (
	"crypto/rand"
	"math/big"
	mathrand "math/rand"
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

// getRng 返回可播种的 PRNG（懒初始化；未显式播种时用时间 + 加密随机数播种）
func (e *Env) getRng() *mathrand.Rand {
	if e.rng == nil {
		seed := time.Now().UnixNano() ^ int64(cryptoRandIntn(1<<30))
		e.rng = mathrand.New(mathrand.NewSource(seed))
	}
	return e.rng
}

func init() {
	// rand / mt_rand / random_int
	builtins["rand"] = func(e *Env, a []Value) (Value, error) {
		rng := e.getRng()
		if len(a) >= 2 {
			lo := a[0].ToInt()
			hi := a[1].ToInt()
			if lo > hi {
				lo, hi = hi, lo
			}
			rangeVal := hi - lo + 1
			return NewInt(lo + rng.Int63n(rangeVal)), nil
		}
		return NewInt(rng.Int63n(2147483647)), nil
	}

	builtins["mt_rand"] = builtins["rand"]

	// srand / mt_srand：显式设置随机种子（PHP 语义；seed=0 时用当前时间自动播种）
	builtins["srand"] = func(e *Env, a []Value) (Value, error) {
		seed := int64(0)
		if len(a) >= 1 {
			seed = a[0].ToInt()
		}
		if seed == 0 {
			seed = time.Now().UnixNano()
		}
		e.rng = mathrand.New(mathrand.NewSource(seed))
		return NewNull(), nil
	}
	builtins["mt_srand"] = builtins["srand"]

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
		// usleep(微秒)：真正睡眠，不阻塞其他并发请求（Go 的 time.Sleep 按 goroutine 挂起）
		us := int64(0)
		if len(a) >= 1 {
			us = a[0].ToInt()
		}
		if us > 0 {
			time.Sleep(time.Duration(us) * time.Microsecond)
		}
		return NewNull(), nil
	}

	builtins["sleep"] = func(e *Env, a []Value) (Value, error) {
		// sleep(秒)：真正睡眠（PHP 语义，成功返回 0）
		sec := int64(0)
		if len(a) >= 1 {
			sec = a[0].ToInt()
		}
		if sec > 0 {
			time.Sleep(time.Duration(sec) * time.Second)
		}
		return NewInt(0), nil
	}
}
