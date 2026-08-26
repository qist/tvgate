package phpgo

import (
	"net/url"
	"strconv"
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
		vals := url.Values{}
		for _, k := range a[0].Keys {
			v := a[0].Arr[k]
			if v.Kind == KindArray {
				for i, subK := range v.Keys {
					vals.Set(k+"["+strconv.Itoa(i)+"]", v.Arr[subK].ToString())
				}
			} else {
				vals.Set(k, v.ToString())
			}
		}
		return NewString(vals.Encode()), nil
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
