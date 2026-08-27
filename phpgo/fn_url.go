package phpgo

import (
	"net/url"
	"strconv"
	"strings"
)

func init() {
	builtins["rawurldecode"] = func(e *Env, a []Value) (Value, error) {
		s, err := url.PathUnescape(a[0].ToString())
		if err != nil {
			return NewString(a[0].ToString()), nil
		}
		return NewString(s), nil
	}
	builtins["urldecode"] = func(e *Env, a []Value) (Value, error) {
		s, err := url.QueryUnescape(a[0].ToString())
		if err != nil {
			return NewString(a[0].ToString()), nil
		}
		return NewString(s), nil
	}
	builtins["http_build_query"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray {
			return NewString(""), nil
		}
		// 第4个参数 encoding_type：PHP_QUERY_RFC3986 (用 %20) 或默认 PHP_QUERY_RFC1738 (用 +)
		// PHP_QUERY_RFC1738 = 1, PHP_QUERY_RFC3986 = 2
		useRFC3986 := false
		if len(a) >= 4 {
			encType := a[3].ToInt()
			if encType == 2 {
				useRFC3986 = true
			}
		}
		var parts []string
		for _, k := range a[0].Keys {
			v := a[0].Arr[k]
			if v.Kind == KindArray {
				for i, subK := range v.Keys {
					encodedKey := url.QueryEscape(k + "[" + strconv.Itoa(i) + "]")
					if useRFC3986 {
						encodedKey = strings.ReplaceAll(encodedKey, "+", "%20")
					}
					encodedVal := url.QueryEscape(v.Arr[subK].ToString())
					if useRFC3986 {
						encodedVal = strings.ReplaceAll(encodedVal, "+", "%20")
					}
					parts = append(parts, encodedKey+"="+encodedVal)
				}
			} else {
				encodedKey := url.QueryEscape(k)
				if useRFC3986 {
					encodedKey = strings.ReplaceAll(encodedKey, "+", "%20")
				}
				encodedVal := url.QueryEscape(v.ToString())
				if useRFC3986 {
					encodedVal = strings.ReplaceAll(encodedVal, "+", "%20")
				}
				parts = append(parts, encodedKey+"="+encodedVal)
			}
		}
		return NewString(strings.Join(parts, "&")), nil
	}
	builtins["parse_url"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		u, err := url.Parse(a[0].ToString())
		if err != nil {
			return NewBool(false), nil
		}
		result := NewArray()
		result.ArraySet(NewString("scheme"), NewString(u.Scheme))
		result.ArraySet(NewString("host"), NewString(u.Hostname()))
		if u.Port() != "" {
			result.ArraySet(NewString("port"), NewInt(parsePort(u.Port())))
		}
		if u.User.Username() != "" {
			result.ArraySet(NewString("user"), NewString(u.User.Username()))
		}
		if u.Path != "" {
			result.ArraySet(NewString("path"), NewString(u.Path))
		}
		if u.RawQuery != "" {
			result.ArraySet(NewString("query"), NewString(u.RawQuery))
		}
		if u.Fragment != "" {
			result.ArraySet(NewString("fragment"), NewString(u.Fragment))
		}
		return result, nil
	}
	builtins["parse_str"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewNull(), nil
		}
		vals, err := url.ParseQuery(a[0].ToString())
		if err != nil {
			return NewNull(), nil
		}
		if len(a) >= 2 {
			arr := NewArray()
			for k, vs := range vals {
				if len(vs) == 1 {
					arr.ArraySet(NewString(k), NewString(vs[0]))
				} else {
					sub := NewArray()
					for i, v := range vs {
						sub.ArraySet(NewInt(int64(i)), NewString(v))
					}
					arr.ArraySet(NewString(k), sub)
				}
			}
			writeRef(e, a[1], arr)
		} else {
			for k, vs := range vals {
				e.vars[k] = NewString(vs[0])
				e.globals[k] = NewString(vs[0])
			}
		}
		return NewNull(), nil
	}
}
