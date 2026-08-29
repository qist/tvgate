package phpgo

import (
	"math"
	"strconv"
)

func init() {
	builtins["exp"] = func(e *Env, a []Value) (Value, error) {
		return NewFloat(math.Exp(a[0].ToFloat())), nil
	}
	builtins["log"] = func(e *Env, a []Value) (Value, error) {
		x := a[0].ToFloat()
		base := math.E
		if len(a) >= 2 && a[1].ToFloat() != 0 {
			base = a[1].ToFloat()
		}
		if x <= 0 {
			return NewFloat(math.NaN()), nil
		}
		return NewFloat(math.Log(x) / math.Log(base)), nil
	}
	builtins["log10"] = func(e *Env, a []Value) (Value, error) {
		if a[0].ToFloat() <= 0 {
			return NewFloat(math.NaN()), nil
		}
		return NewFloat(math.Log10(a[0].ToFloat())), nil
	}
	builtins["log2"] = func(e *Env, a []Value) (Value, error) {
		if a[0].ToFloat() <= 0 {
			return NewFloat(math.NaN()), nil
		}
		return NewFloat(math.Log2(a[0].ToFloat())), nil
	}
	builtins["log1p"] = func(e *Env, a []Value) (Value, error) {
		if a[0].ToFloat() <= -1 {
			return NewFloat(math.NaN()), nil
		}
		return NewFloat(math.Log1p(a[0].ToFloat())), nil
	}
	builtins["fmod"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 || a[1].ToFloat() == 0 {
			return NewFloat(math.NaN()), nil
		}
		return NewFloat(math.Mod(a[0].ToFloat(), a[1].ToFloat())), nil
	}
	builtins["deg2rad"] = func(e *Env, a []Value) (Value, error) {
		return NewFloat(a[0].ToFloat() * math.Pi / 180), nil
	}
	builtins["rad2deg"] = func(e *Env, a []Value) (Value, error) {
		return NewFloat(a[0].ToFloat() * 180 / math.Pi), nil
	}
	builtins["sin"] = func(e *Env, a []Value) (Value, error)  { return NewFloat(math.Sin(a[0].ToFloat())), nil }
	builtins["cos"] = func(e *Env, a []Value) (Value, error)  { return NewFloat(math.Cos(a[0].ToFloat())), nil }
	builtins["tan"] = func(e *Env, a []Value) (Value, error)  { return NewFloat(math.Tan(a[0].ToFloat())), nil }
	builtins["asin"] = func(e *Env, a []Value) (Value, error) { return NewFloat(math.Asin(a[0].ToFloat())), nil }
	builtins["acos"] = func(e *Env, a []Value) (Value, error) { return NewFloat(math.Acos(a[0].ToFloat())), nil }
	builtins["atan"] = func(e *Env, a []Value) (Value, error) { return NewFloat(math.Atan(a[0].ToFloat())), nil }
	builtins["atan2"] = func(e *Env, a []Value) (Value, error) {
		return NewFloat(math.Atan2(a[0].ToFloat(), a[1].ToFloat())), nil
	}
	builtins["sinh"] = func(e *Env, a []Value) (Value, error) { return NewFloat(math.Sinh(a[0].ToFloat())), nil }
	builtins["cosh"] = func(e *Env, a []Value) (Value, error) { return NewFloat(math.Cosh(a[0].ToFloat())), nil }
	builtins["tanh"] = func(e *Env, a []Value) (Value, error) { return NewFloat(math.Tanh(a[0].ToFloat())), nil }
	builtins["hypot"] = func(e *Env, a []Value) (Value, error) {
		return NewFloat(math.Hypot(a[0].ToFloat(), a[1].ToFloat())), nil
	}
	builtins["decoct"] = func(e *Env, a []Value) (Value, error) {
		return NewString(strconv.FormatInt(a[0].ToInt(), 8)), nil
	}
	builtins["octdec"] = func(e *Env, a []Value) (Value, error) {
		n, _ := strconv.ParseInt(a[0].ToString(), 8, 64)
		return NewInt(n), nil
	}
	builtins["srand"] = func(e *Env, a []Value) (Value, error)  { return NewNull(), nil }
	builtins["mt_srand"] = func(e *Env, a []Value) (Value, error) { return NewNull(), nil }
	builtins["is_finite"] = func(e *Env, a []Value) (Value, error) {
		return NewBool(!math.IsInf(a[0].ToFloat(), 0) && !math.IsNaN(a[0].ToFloat())), nil
	}
	builtins["is_infinite"] = func(e *Env, a []Value) (Value, error) {
		return NewBool(math.IsInf(a[0].ToFloat(), 0)), nil
	}
	builtins["is_nan"] = func(e *Env, a []Value) (Value, error) {
		return NewBool(math.IsNaN(a[0].ToFloat())), nil
	}
}
