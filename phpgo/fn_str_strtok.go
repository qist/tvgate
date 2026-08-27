package phpgo

import "strings"

// strtokState 保存 strtok 的内部状态（模拟 PHP 的 strtok 静态变量）
var strtokState struct {
	str   string
	pos   int
}

func init() {
	builtins["strtok"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewBool(false), nil
		}
		if len(a) >= 2 {
			// strtok($str, $token) — 新的分割
			strtokState.str = a[0].ToString()
			strtokState.pos = 0
			return strtokNext(a[1].ToString()), nil
		}
		// strtok($token) — 继续上次的分割
		return strtokNext(a[0].ToString()), nil
	}
}

// strtokNext 查找下一个 token
func strtokNext(tokens string) Value {
	s := strtokState.str
	if strtokState.pos >= len(s) {
		return NewBool(false)
	}
	rest := s[strtokState.pos:]
	// 找到第一个分隔符的位置
	idx := strings.IndexAny(rest, tokens)
	if idx < 0 {
		// 没有更多分隔符，返回剩余部分
		result := rest
		strtokState.pos = len(s)
		return NewString(result)
	}
	result := rest[:idx]
	// 跳过连续的分隔符
	strtokState.pos += idx + 1
	for strtokState.pos < len(s) && strings.IndexByte(tokens, s[strtokState.pos]) >= 0 {
		strtokState.pos++
	}
	if result == "" {
		// 如果 token 为空，尝试下一个
		if strtokState.pos >= len(s) {
			return NewBool(false)
		}
		return strtokNext(tokens)
	}
	return NewString(result)
}
