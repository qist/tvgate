package phpgo

import "strings"

func init() {
	builtins["strtolower"] = func(e *Env, a []Value) (Value, error) {
		return NewString(strings.ToLower(a[0].ToString())), nil
	}
	builtins["strtoupper"] = func(e *Env, a []Value) (Value, error) {
		return NewString(strings.ToUpper(a[0].ToString())), nil
	}
	// mb_* 别名
	builtins["mb_strtolower"] = builtins["strtolower"]
	builtins["mb_strtoupper"] = builtins["strtoupper"]
	builtins["mb_strlen"] = builtins["strlen"]
	builtins["mb_substr"] = builtins["substr"]
	builtins["mb_strpos"] = builtins["strpos"]
	builtins["mb_stripos"] = builtins["stripos"]
}
