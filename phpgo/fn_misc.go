package phpgo

import (
	"strconv"
	"strings"
)

func init() {
	// 类型检查
	builtins["is_array"] = func(e *Env, a []Value) (Value, error) {
		return NewBool(a[0].Kind == KindArray), nil
	}
	builtins["is_string"] = func(e *Env, a []Value) (Value, error) {
		return NewBool(a[0].Kind == KindString), nil
	}
	builtins["is_int"] = func(e *Env, a []Value) (Value, error) {
		return NewBool(a[0].Kind == KindInt), nil
	}
	builtins["is_integer"] = builtins["is_int"]
	builtins["is_float"] = func(e *Env, a []Value) (Value, error) {
		return NewBool(a[0].Kind == KindFloat), nil
	}
	builtins["is_double"] = builtins["is_float"]
	builtins["is_bool"] = func(e *Env, a []Value) (Value, error) {
		return NewBool(a[0].Kind == KindBool), nil
	}
	builtins["is_null"] = func(e *Env, a []Value) (Value, error) {
		return NewBool(a[0].Kind == KindNull), nil
	}
	builtins["is_numeric"] = func(e *Env, a []Value) (Value, error) {
		switch a[0].Kind {
		case KindInt, KindFloat:
			return NewBool(true), nil
		case KindString:
			s := strings.TrimSpace(a[0].Str)
			if s == "" {
				return NewBool(false), nil
			}
			_, err := strconvParseFloat(s)
			return NewBool(err == nil), nil
		}
		return NewBool(false), nil
	}

	// 输出控制
	builtins["ob_start"] = func(e *Env, a []Value) (Value, error) {
		e.obStack = append(e.obStack, &strings.Builder{})
		return NewBool(true), nil
	}
	builtins["ob_get_clean"] = func(e *Env, a []Value) (Value, error) {
		if len(e.obStack) == 0 {
			return NewString(""), nil
		}
		last := e.obStack[len(e.obStack)-1]
		e.obStack = e.obStack[:len(e.obStack)-1]
		return NewString(last.String()), nil
	}
	builtins["ob_get_contents"] = func(e *Env, a []Value) (Value, error) {
		if len(e.obStack) == 0 {
			return NewString(""), nil
		}
		return NewString(e.obStack[len(e.obStack)-1].String()), nil
	}
	builtins["ob_end_clean"] = func(e *Env, a []Value) (Value, error) {
		if len(e.obStack) > 0 {
			e.obStack = e.obStack[:len(e.obStack)-1]
		}
		return NewBool(true), nil
	}
	builtins["ob_flush"] = func(e *Env, a []Value) (Value, error) {
		return NewNull(), nil
	}
	builtins["ob_get_level"] = func(e *Env, a []Value) (Value, error) {
		return NewInt(int64(len(e.obStack))), nil
	}
	builtins["ob_implicit_flush"] = func(e *Env, a []Value) (Value, error) {
		return NewNull(), nil
	}
	builtins["flush"] = func(e *Env, a []Value) (Value, error) {
		return NewNull(), nil
	}

	// error/ini
	builtins["error_reporting"] = func(e *Env, a []Value) (Value, error) {
		return NewInt(0), nil
	}
	builtins["ini_set"] = func(e *Env, a []Value) (Value, error) {
		return NewString(""), nil
	}
	builtins["ini_get"] = func(e *Env, a []Value) (Value, error) {
		return NewString(""), nil
	}
	builtins["php_sapi_name"] = func(e *Env, a []Value) (Value, error) {
		return NewString("fpm-fcgi"), nil
	}
	builtins["phpinfo"] = func(e *Env, a []Value) (Value, error) {
		return NewBool(true), nil
	}

	// session/cookie
	builtins["session_start"] = func(e *Env, a []Value) (Value, error) {
		return NewBool(true), nil
	}
	builtins["session_id"] = func(e *Env, a []Value) (Value, error) {
		return NewString(""), nil
	}
	builtins["setcookie"] = func(e *Env, a []Value) (Value, error) {
		return NewBool(true), nil
	}

	// json_last_error
	builtins["json_last_error"] = func(e *Env, a []Value) (Value, error) {
		return NewInt(0), nil
	}
	builtins["json_last_error_msg"] = func(e *Env, a []Value) (Value, error) {
		return NewString("No error"), nil
	}

	// error_log
	builtins["error_log"] = func(e *Env, a []Value) (Value, error) {
		return NewBool(true), nil
	}

	// set_time_limit
	builtins["set_time_limit"] = func(e *Env, a []Value) (Value, error) {
		return NewBool(true), nil
	}

	// getenv
	builtins["getenv"] = func(e *Env, a []Value) (Value, error) {
		return NewString(""), nil
	}
}

func strconvParseFloat(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
}
