package phpgo

import (
	"math/big"
	"os"
	"strings"
)

// 参考 PHP bcmath 扩展实现（默认 scale=0，即整型运算）。
// 覆盖 TVGate phpgo 现有 PHP 脚本（如 kankanews wxty.php）RSA 解密所需的
// bcadd/bcsub/bcmul/bcdiv/bcmod/bcpowmain/bcpowmod/bccomp，以及 system temp dir。
func init() {
	// sys_get_temp_dir：返回系统临时目录
	builtins["sys_get_temp_dir"] = func(e *Env, a []Value) (Value, error) {
		return NewString(os.TempDir()), nil
	}

	// 解析十进制字符串（含小数/分数）为大数比
	bcRat := func(v Value) *big.Rat {
		s := strings.TrimSpace(v.ToString())
		if s == "" {
			return new(big.Rat)
		}
		if r, ok := new(big.Rat).SetString(s); ok {
			return r
		}
		if n, ok := new(big.Int).SetString(s, 10); ok {
			return new(big.Rat).SetInt(n)
		}
		return new(big.Rat)
	}

	// 解析为整数（用于 bcpow/bcpowmod/bcmod）
	bcInt := func(v Value) *big.Int {
		s := strings.TrimSpace(v.ToString())
		if i := strings.IndexByte(s, '.'); i >= 0 {
			s = s[:i]
		}
		n := new(big.Int)
		if _, ok := n.SetString(s, 10); !ok {
			return big.NewInt(0)
		}
		return n
	}

	// 读取可选 scale 参数（缺省 0）
	bcScale := func(a []Value) int {
		if len(a) >= 3 {
			return int(a[2].ToInt())
		}
		return 0
	}

	// 把比按 scale 位小数转字符串（scale<=0 时截断为整数，向零舍入，与 PHP bcdiv 一致）
	bcFmt := func(r *big.Rat, scale int) string {
		q := new(big.Int)
		if scale <= 0 {
			q.Quo(r.Num(), r.Denom()) // 向零截断
			return q.String()
		}
		pow := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
		q.Mul(r.Num(), pow)
		q.Quo(q, r.Denom())
		neg := q.Sign() < 0
		s := new(big.Int).Abs(q).String()
		for len(s) <= scale {
			s = "0" + s
		}
		ip, fp := s[:len(s)-scale], s[len(s)-scale:]
		out := ip + "." + fp
		if neg && ip != "0" {
			out = "-" + out
		}
		return out
	}

	builtins["bcadd"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewString("0"), nil
		}
		scale := bcScale(a)
		res := new(big.Rat).Add(bcRat(a[0]), bcRat(a[1]))
		return NewString(bcFmt(res, scale)), nil
	}
	builtins["bcsub"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewString("0"), nil
		}
		scale := bcScale(a)
		res := new(big.Rat).Sub(bcRat(a[0]), bcRat(a[1]))
		return NewString(bcFmt(res, scale)), nil
	}
	builtins["bcmul"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewString("0"), nil
		}
		scale := bcScale(a)
		res := new(big.Rat).Mul(bcRat(a[0]), bcRat(a[1]))
		return NewString(bcFmt(res, scale)), nil
	}
	builtins["bcdiv"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewString("0"), nil
		}
		scale := bcScale(a)
		den := bcRat(a[1])
		if den.Sign() == 0 {
			return NewBool(false), nil // PHP: 除零错误
		}
		res := new(big.Rat).Quo(bcRat(a[0]), den)
		return NewString(bcFmt(res, scale)), nil
	}
	builtins["bcmod"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewString("0"), nil
		}
		n, m := bcInt(a[0]), bcInt(a[1])
		if m.Sign() == 0 {
			return NewBool(false), nil
		}
		return NewString(new(big.Int).Mod(n, m).String()), nil
	}
	builtins["bcpow"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewString("0"), nil
		}
		exp := bcInt(a[1])
		scale := bcScale(a)
		if exp.Sign() < 0 {
			return NewString("0"), nil
		}
		// 用整数快速幂（scale=0 通用场景），base 取整
		r := new(big.Int).Exp(bcInt(a[0]), exp, nil)
		return NewString(bcFmt(new(big.Rat).SetInt(r), scale)), nil
	}
	builtins["bcpowmod"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 3 {
			return NewString("0"), nil
		}
		base := bcInt(a[0])
		exp := bcInt(a[1])
		mod := bcInt(a[2])
		if mod.Sign() == 0 {
			return NewString("0"), nil
		}
		r := new(big.Int).Exp(base, exp, mod)
		return NewString(r.String()), nil
	}
	builtins["bccomp"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewInt(0), nil
		}
		return NewInt(int64(bcRat(a[0]).Cmp(bcRat(a[1])))), nil
	}
}
