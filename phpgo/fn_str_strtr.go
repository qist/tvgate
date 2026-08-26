package phpgo

import "strings"

func init() {
	builtins["strtr"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewString(""), nil
		}
		s := a[0].ToString()
		if a[1].Kind == KindArray {
			pairs := map[string]string{}
			keys := []string{}
			for _, k := range a[1].Keys {
				pairs[k] = a[1].Arr[k].ToString()
				keys = append(keys, k)
			}
			sortStringsByLen(keys)
			for _, k := range keys {
				s = strings.ReplaceAll(s, k, pairs[k])
			}
			return NewString(s), nil
		}
		if len(a) >= 3 {
			r := strings.NewReplacer(a[1].ToString(), a[2].ToString())
			return NewString(r.Replace(s)), nil
		}
		return NewString(s), nil
	}
}
