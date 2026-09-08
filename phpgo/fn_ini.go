package phpgo

// parse_ini_file / parse_ini_string。
// 支持：分节 [section]、key = value、';'/'#' 注释、单双引号值、
// INI_SCANNER_TYPED 的类型转换（true/false/null/on/off/yes/no/数字）。

import (
	"os"
	"strconv"
	"strings"
)

// phpParseINI 解析 ini 文本。processSections=true 时按节组织；scanner 为 INI_SCANNER_* 值。
func phpParseINI(src string, processSections bool, scanner int64) Value {
	result := NewArray()
	cur := &result // 当前写入目标
	if processSections {
		cur = nil // 无节时顶层写入 result
	}
	lines := strings.Split(src, "\n")
	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed[0] == ';' || trimmed[0] == '#' {
			continue
		}
		// 节头
		if trimmed[0] == '[' && strings.HasSuffix(trimmed, "]") {
			secName := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
			if secName != "" {
				sec := NewArray()
				result.ArraySet(NewString(secName), sec)
				if processSections {
					cur = &sec
				}
			}
			continue
		}
		eq := strings.IndexByte(trimmed, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:eq])
		valStr := strings.TrimSpace(trimmed[eq+1:])
		if key == "" {
			continue
		}
		val := phpIniValue(valStr, scanner)
		target := &result
		if cur != nil {
			target = cur
		}
		// 同节同 key 重复出现 → 转为数组（PHP 行为）
		if existing, has := target.Arr[key]; has {
			combined := NewArray()
			combined.ArraySet(NewInt(0), existing)
			combined.ArraySet(NewInt(1), val)
			target.ArraySet(NewString(key), combined)
		} else {
			target.ArraySet(NewString(key), val)
		}
	}
	return result
}

// phpIniValue 解析单个 ini 值
func phpIniValue(s string, scanner int64) Value {
	s = strings.TrimSpace(s)
	// 去掉未加引号值行内注释（; 或 #），但值前部需排除引号内
	if len(s) > 0 && s[0] != '"' && s[0] != '\'' {
		if i := strings.IndexAny(s, ";#"); i >= 0 {
			s = strings.TrimSpace(s[:i])
		}
	}
	if len(s) >= 2 {
		if s[0] == '"' && s[len(s)-1] == '"' {
			inner := s[1 : len(s)-1]
			// PHP 引号内支持 \" 转义
			inner = strings.ReplaceAll(inner, `\"`, `"`)
			return NewString(inner)
		}
		if s[0] == '\'' && s[len(s)-1] == '\'' {
			return NewString(s[1 : len(s)-1])
		}
	}
	if scanner != 2 { // 非 TYPED：返回字符串
		return NewString(s)
	}
	// INI_SCANNER_TYPED
	switch strings.ToLower(s) {
	case "true", "yes", "on":
		return NewBool(true)
	case "false", "no", "off":
		return NewBool(false)
	case "null", "none":
		return NewNull()
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return NewInt(n)
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return NewFloat(f)
	}
	return NewString(s)
}

func init() {
	builtins["parse_ini_string"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		process := false
		if len(a) >= 2 {
			process = a[1].ToBool()
		}
		scanner := int64(0)
		if len(a) >= 3 {
			scanner = a[2].ToInt()
		}
		return phpParseINI(a[0].ToString(), process, scanner), nil
	}
	builtins["parse_ini_file"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		path := e.ResolvePath(a[0].ToString())
		data, err := os.ReadFile(path)
		if err != nil {
			return NewBool(false), nil
		}
		process := false
		if len(a) >= 2 {
			process = a[1].ToBool()
		}
		scanner := int64(0)
		if len(a) >= 3 {
			scanner = a[2].ToInt()
		}
		return phpParseINI(string(data), process, scanner), nil
	}
}
