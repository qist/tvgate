package phpgo

func init() {
	builtins["preg_replace_callback"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 3 {
			return NewNull(), nil
		}
		pattern := a[0].ToString()
		subj := a[2].ToString()

		re, err := compilePHPRegex(pattern)
		if err != nil {
			return NewString(subj), nil
		}

		result := re.ReplaceAllStringFunc(subj, func(match string) string {
			subs := re.FindStringSubmatch(match)
			// PHP 语义：回调接收一个 $matches 数组（$matches[0] 为完整匹配）
			matches := NewArray()
			for i, s := range subs {
				matches.ArraySet(NewInt(int64(i)), NewString(s))
			}
			// 支持字符串函数名与闭包/对象方法（callCallable 统一处理）
			ret, _ := callCallable(e, a[1], []Value{matches})
			return ret.ToString()
		})
		return NewString(result), nil
	}

	builtins["preg_split"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewArray(), nil
		}
		pattern := a[0].ToString()
		subj := a[1].ToString()
		re, err := compilePHPRegex(pattern)
		if err != nil {
			return NewArray(), nil
		}
		parts := re.Split(subj, -1)
		result := NewArray()
		for i, p := range parts {
			result.ArraySet(NewInt(int64(i)), NewString(p))
		}
		return result, nil
	}

	// preg_replace_callback_array：按 [pattern => callback, ...] 顺序逐条替换
	// PHP: preg_replace_callback_array(array $patterns_and_callbacks, string|array $subject, int $limit=-1, int &$count=null)
	builtins["preg_replace_callback_array"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 || a[0].Kind != KindArray {
			return NewNull(), nil
		}
		cbMap := a[0]
		subj := a[1]
		limit := int64(-1)
		if len(a) >= 3 {
			limit = a[2].ToInt()
		}
		count := int64(0)
		replaceOne := func(s string) string {
			out := s
			for _, k := range cbMap.Keys {
				re, err := compilePHPRegex(k)
				if err != nil {
					continue
				}
				if limit >= 0 && count >= limit {
					break
				}
				out = re.ReplaceAllStringFunc(out, func(match string) string {
					if limit >= 0 && count >= limit {
						return match // 已达替换上限，保持原样
					}
					subs := re.FindStringSubmatch(match)
					// PHP 语义：回调接收一个 $matches 数组
					matches := NewArray()
					for i, ss := range subs {
						matches.ArraySet(NewInt(int64(i)), NewString(ss))
					}
					ret, _ := callCallable(e, cbMap.Arr[k], []Value{matches})
					count++
					return ret.ToString()
				})
			}
			return out
		}
		var result Value
		if subj.Kind == KindArray {
			result = NewArray()
			for _, k := range subj.Keys {
				result.ArraySet(NewString(k), NewString(replaceOne(subj.Arr[k].ToString())))
			}
		} else {
			result = NewString(replaceOne(subj.ToString()))
		}
		// 第 4 个参数 &$count 按引用写回
		if len(a) >= 4 {
			writeRef(e, a[3], NewInt(count))
		}
		return result, nil
	}
}
