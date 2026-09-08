package phpgo

// HTTP 相关补齐：headers_sent / header_remove / header_register_callback / getallheaders。
// phpgo 的输出统一在脚本结束后下发，因此 headers_sent 用「是否已有脱离输出缓冲的内容」近似。

import "strings"

func init() {
	builtins["headers_sent"] = func(e *Env, a []Value) (Value, error) {
		// 有已刷出的输出且无活动输出缓冲 → 认为响应头已发送
		return NewBool(len(e.obStack) == 0 && e.echoOut.Len() > 0), nil
	}
	builtins["header_remove"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			e.headers = nil
			return NewNull(), nil
		}
		name := strings.ToLower(strings.TrimSpace(a[0].ToString()))
		out := e.headers[:0]
		for _, h := range e.headers {
			colon := strings.IndexByte(h, ':')
			if colon > 0 && strings.ToLower(strings.TrimSpace(h[:colon])) == name {
				continue
			}
			out = append(out, h)
		}
		e.headers = out
		return NewNull(), nil
	}
	builtins["header_register_callback"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		e.headerCallbacks = append(e.headerCallbacks, a[0])
		return NewBool(true), nil
	}
	// getallheaders()：从 $_SERVER 的 HTTP_* 重建请求头（键名规范化）
	builtins["getallheaders"] = func(e *Env, a []Value) (Value, error) {
		arr := NewArray()
		for k, v := range e.server {
			name := ""
			if strings.HasPrefix(k, "HTTP_") {
				name = k[5:]
			} else if k == "CONTENT_TYPE" {
				name = "Content-Type"
			} else if k == "CONTENT_LENGTH" {
				name = "Content-Length"
			} else {
				continue
			}
			if name != "Content-Type" && name != "Content-Length" {
				parts := strings.Split(name, "_")
				for i, p := range parts {
					parts[i] = strings.Title(strings.ToLower(p))
				}
				name = strings.Join(parts, "-")
			}
			arr.ArraySet(NewString(name), NewString(v))
		}
		return arr, nil
	}
}
