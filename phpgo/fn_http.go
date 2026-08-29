package phpgo

import (
	"fmt"
	"net/http"
	"strings"
)

func init() {
	// get_headers：获取 URL 的响应头列表；第二个参数 true 返回关联数组
	builtins["get_headers"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		assoc := false
		if len(a) >= 2 {
			assoc = a[1].ToBool()
		}
		if e.proxy == nil {
			return NewBool(false), nil
		}
		result, err := e.proxy("GET", a[0].ToString(), &CurlOptions{FollowRedirect: true})
		if err != nil {
			return NewBool(false), nil
		}
		if assoc {
			arr := NewArray()
			arr.ArraySet(NewString("http_code"), NewInt(int64(result.StatusCode)))
			arr.ArraySet(NewString("Effective-URL"), NewString(result.EffectiveURL))
			for _, h := range result.Headers {
				if i := strings.IndexByte(h, ':'); i > 0 {
					arr.ArraySet(NewString(strings.TrimSpace(h[:i])), NewString(strings.TrimSpace(h[i+1:])))
				}
			}
			return arr, nil
		}
		arr := NewArray()
		arr.ArraySet(NewInt(0), NewString(fmt.Sprintf("HTTP/1.1 %d %s", result.StatusCode, http.StatusText(result.StatusCode))))
		for _, h := range result.Headers {
			arr.ArraySet(NewInt(int64(len(arr.Keys))), NewString(h))
		}
		return arr, nil
	}
}
