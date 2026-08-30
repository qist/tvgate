package phpgo

import (
	"os"
	"strconv"
	"strings"
	"time"
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
		// PHP 语义：把最顶层输出缓冲的内容刷到下一层（或最终输出）
		if len(e.obStack) > 0 {
			top := e.obStack[len(e.obStack)-1].String()
			e.obStack[len(e.obStack)-1].Reset()
			if len(e.obStack) > 1 {
				e.obStack[len(e.obStack)-2].WriteString(top)
			} else {
				e.echoOut.WriteString(top)
			}
		}
		return NewNull(), nil
	}
	builtins["ob_get_level"] = func(e *Env, a []Value) (Value, error) {
		return NewInt(int64(len(e.obStack))), nil
	}
	builtins["ob_implicit_flush"] = func(e *Env, a []Value) (Value, error) {
		// 标记隐式刷新（phpgo 输出统一在脚本结束后下发，此开关不改变收集行为）
		if len(a) >= 1 {
			e.implicitFlush = a[0].ToBool()
		}
		return NewNull(), nil
	}
	builtins["flush"] = func(e *Env, a []Value) (Value, error) {
		// phpgo 输出在脚本执行结束后统一下发，因此无需（也无法）中途推流；
		// 这里把仍在缓冲里的内容合并到最终输出，保证调用 flush 后语义正确
		for len(e.obStack) > 0 {
			top := e.obStack[len(e.obStack)-1].String()
			e.obStack[len(e.obStack)-1].Reset()
			e.echoOut.WriteString(top)
			e.obStack = e.obStack[:len(e.obStack)-1]
		}
		return NewNull(), nil
	}

	// error/ini
	builtins["error_reporting"] = func(e *Env, a []Value) (Value, error) {
		old := e.errorLevel
		if len(a) >= 1 {
			e.errorLevel = a[0].ToInt()
		}
		return NewInt(old), nil
	}
	builtins["ini_set"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		key := a[0].ToString()
		old, has := e.ini[key]
		e.ini[key] = a[1].ToString()
		if has {
			return NewString(old), nil
		}
		return NewBool(false), nil
	}
	builtins["ini_get"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		if v, ok := e.ini[a[0].ToString()]; ok {
			return NewString(v), nil
		}
		return NewString(""), nil
	}
	builtins["php_sapi_name"] = func(e *Env, a []Value) (Value, error) {
		return NewString("cli-server"), nil
	}
	builtins["phpinfo"] = func(e *Env, a []Value) (Value, error) {
		// 输出最小化 phpinfo 信息（纯 Go 运行时，无真实 PHP 版本）
		e.echoOut.WriteString("<html><head><title>phpinfo()</title></head><body>")
		e.echoOut.WriteString("<h1>phpgo - Pure Go PHP Runtime</h1>")
		e.echoOut.WriteString("<table border=\"1\" cellpadding=\"3\"><tr><td>PHP Version (simulated)</td><td>8.x (phpgo)</td></tr>")
		e.echoOut.WriteString("<tr><td>PHP API</td><td>20220829</td></tr>")
		e.echoOut.WriteString("<tr><td>SAPI</td><td>cli-server</td></tr>")
		e.echoOut.WriteString("<tr><td>Server Software</td><td>TVGate / phpgo</td></tr>")
		e.echoOut.WriteString("<tr><td>Built-in Functions</td><td>300+ (pure Go)</td></tr></table>")
		e.echoOut.WriteString("</body></html>")
		return NewBool(true), nil
	}

	// session/cookie
	builtins["session_start"] = func(e *Env, a []Value) (Value, error) {
		// 最小化会话：已有 PHPSESSID 则复用，否则生成并下发 Set-Cookie；
		// $_SESSION 为请求内数组，脚本可读写
		if e.sessionID == "" {
			if c, ok := e.cookie["PHPSESSID"]; ok && c != "" {
				e.sessionID = c
			} else {
				e.sessionID = "phpgo" + strconv.FormatInt(time.Now().UnixNano(), 16) + strconv.Itoa(cryptoRandIntn(0xFFFFFF))
				e.headers = append(e.headers, "Set-Cookie: PHPSESSID="+e.sessionID+"; path=/")
			}
		}
		return NewBool(true), nil
	}
	builtins["session_id"] = func(e *Env, a []Value) (Value, error) {
		old := e.sessionID
		if len(a) >= 1 {
			e.sessionID = a[0].ToString()
		}
		return NewString(old), nil
	}
	// setcookie：真正发送 Set-Cookie 响应头（支持 PHP 7.3+ options 数组与传统多参数两种形式）
	builtins["setcookie"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		name := a[0].ToString()
		value := ""
		if len(a) >= 2 {
			value = a[1].ToString()
		}
		parts := []string{name + "=" + value}
		if len(a) >= 3 && a[2].Kind == KindArray {
			opts := a[2]
			getStr := func(k string) string {
				v := opts.ArrayGet(NewString(k))
				if v.Kind == KindNull {
					return ""
				}
				return v.ToString()
			}
			if exp := opts.ArrayGet(NewString("expires")); exp.Kind != KindNull && exp.ToInt() > 0 {
				parts = append(parts, "expires="+cookieExpiresStr(exp.ToInt()))
			}
			if p := getStr("path"); p != "" {
				parts = append(parts, "path="+p)
			}
			if d := getStr("domain"); d != "" {
				parts = append(parts, "domain="+d)
			}
			if opts.ArrayGet(NewString("secure")).ToBool() {
				parts = append(parts, "Secure")
			}
			if opts.ArrayGet(NewString("httponly")).ToBool() {
				parts = append(parts, "HttpOnly")
			}
			if s := getStr("samesite"); s != "" {
				parts = append(parts, "SameSite="+s)
			}
		} else {
			expires := int64(0)
			if len(a) >= 3 {
				expires = a[2].ToInt()
			}
			path := "/"
			if len(a) >= 4 && a[3].ToString() != "" {
				path = a[3].ToString()
			}
			domain := ""
			if len(a) >= 5 {
				domain = a[4].ToString()
			}
			secure := len(a) >= 6 && a[5].ToBool()
			httponly := len(a) >= 7 && a[6].ToBool()
			if expires > 0 {
				parts = append(parts, "expires="+cookieExpiresStr(expires))
			}
			if path != "" {
				parts = append(parts, "path="+path)
			}
			if domain != "" {
				parts = append(parts, "domain="+domain)
			}
			if secure {
				parts = append(parts, "Secure")
			}
			if httponly {
				parts = append(parts, "HttpOnly")
			}
		}
		e.headers = append(e.headers, "Set-Cookie: "+strings.Join(parts, "; "))
		return NewBool(true), nil
	}

	// json_last_error / json_last_error_msg：返回最近一次 json_encode/json_decode 的错误
	builtins["json_last_error"] = func(e *Env, a []Value) (Value, error) {
		return NewInt(int64(e.jsonErr)), nil
	}
	builtins["json_last_error_msg"] = func(e *Env, a []Value) (Value, error) {
		if e.jsonErr == 0 {
			return NewString("No error"), nil
		}
		return NewString(e.jsonErrMsg), nil
	}

	// error_log：写入错误日志（默认 stderr；PHP 类型 3 表示写入指定文件）
	builtins["error_log"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		msg := a[0].ToString()
		dst := ""
		if len(a) >= 3 {
			dst = a[2].ToString() // 类型 3 时第 3 参为文件路径
		}
		line := time.Now().Format("2006-01-02 15:04:05") + " [php error_log] " + msg + "\n"
		var err error
		if dst != "" {
			err = os.WriteFile(dst, []byte(line), 0644)
		} else {
			_, err = os.Stderr.WriteString(line)
		}
		return NewBool(err == nil), nil
	}

	// set_time_limit：记录到 ini（纯 Go 运行时无法中途终止脚本执行，仅保留值）
	builtins["set_time_limit"] = func(e *Env, a []Value) (Value, error) {
		if len(a) >= 1 {
			e.ini["max_execution_time"] = strconv.FormatInt(a[0].ToInt(), 10)
		}
		return NewBool(true), nil
	}

	// getenv：优先返回真实进程环境变量，其次 $_ENV（需显式配置）
	builtins["getenv"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		name := a[0].ToString()
		if v, ok := os.LookupEnv(name); ok {
			return NewString(v), nil
		}
		if v, ok := e.envmap[name]; ok {
			return NewString(v), nil
		}
		return NewBool(false), nil
	}
}

func strconvParseFloat(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
}

// cookieExpiresStr 把 unix 时间戳格式化为 HTTP Cookie Expires（PHP setcookie 语义）
func cookieExpiresStr(ts int64) string {
	return time.Unix(ts, 0).UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT")
}
