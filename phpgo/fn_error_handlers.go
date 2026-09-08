package phpgo

import "fmt"

// set_error_handler / restore_error_handler / trigger_error / error_get_last / assert。
//
// phpgo 的内置函数大多以「返回 false」表达可恢复失败，不主动产生 PHP 警告；
// 这里提供与真实 PHP 相同的注册/触发 API，使脚本可拦截并处理用户级错误
// （trigger_error / assert 失败等）。error_get_last 同时记录这些错误。

// phpErrTypeName 错误级别 → PHP 消息里的类型词
func phpErrTypeName(level int64) string {
	switch {
	case level&(1|16|256|32768) != 0: // E_ERROR / E_CORE_ERROR / E_USER_ERROR / E_RECOVERABLE_ERROR
		return "Fatal error"
	case level&(2|32|512) != 0: // E_WARNING / E_CORE_WARNING / E_USER_WARNING
		return "Warning"
	case level&(8|1024) != 0: // E_NOTICE / E_USER_NOTICE
		return "Notice"
	case level&8192 != 0 || level&16384 != 0: // E_DEPRECATED / E_USER_DEPRECATED
		return "Deprecated"
	}
	return "Unknown"
}

// handlePHPError 统一入口：记录到 error_get_last，按需回调已注册处理器或输出。
// 返回是否已由处理器处理（不再走默认显示逻辑）。
func (e *Env) handlePHPError(level int64, msg string) bool {
	e.lastErrLevel = level
	e.lastErrMsg = msg
	// 自定义处理器：仅当其掩码覆盖该级别时回调（PHP 语义：被 @/error_reporting(0) 屏蔽时不调用）
	if e.errorHandler.Kind != KindNull && e.errorMask != 0 && e.errorMask&level != 0 {
		_, _ = callCallable(e, e.errorHandler, []Value{
			NewInt(level), NewString(msg), NewString(e.scriptPath), NewInt(0),
		})
		return true
	}
	// 默认显示逻辑：error_reporting 未设置(0)视为 E_ALL
	if e.errorLevel == 0 || e.errorLevel&level != 0 {
		e.writeOutput("PHP " + phpErrTypeName(level) + ": " + msg + "\n")
	}
	return false
}

func init() {
	// set_error_handler(callable|null, int $error_types = E_ALL)：返回旧处理器（无则 null）
	builtins["set_error_handler"] = func(e *Env, a []Value) (Value, error) {
		prev := NewNull()
		if e.errorHandler.Kind != KindNull {
			prev = e.errorHandler
		}
		old := e.errorHandler
		oldMask := e.errorMask
		// 入栈旧状态供 restore_error_handler 恢复
		entry := NewArray()
		entry.ArraySet(NewInt(0), old)
		entry.ArraySet(NewInt(1), NewInt(oldMask))
		e.errorHandlers = append(e.errorHandlers, entry)

		if len(a) > 0 && a[0].Kind != KindNull {
			e.errorHandler = a[0]
			if len(a) >= 2 {
				e.errorMask = a[1].ToInt()
			} else {
				e.errorMask = 32767 // E_ALL
			}
		} else {
			// null：关闭自定义处理器
			e.errorHandler = NewNull()
			e.errorMask = 0
		}
		return prev, nil
	}
	// restore_error_handler()：恢复上一个处理器
	builtins["restore_error_handler"] = func(e *Env, a []Value) (Value, error) {
		if n := len(e.errorHandlers); n > 0 {
			entry := e.errorHandlers[n-1]
			e.errorHandlers = e.errorHandlers[:n-1]
			old := entry.ArrayGet(NewInt(0))
			mask := entry.ArrayGet(NewInt(1)).ToInt()
			if old.Kind == KindNull {
				e.errorHandler = NewNull()
				e.errorMask = 0
			} else {
				e.errorHandler = old
				e.errorMask = mask
			}
		} else {
			e.errorHandler = NewNull()
			e.errorMask = 0
		}
		return NewBool(true), nil
	}
	// trigger_error(message, error_level = E_USER_NOTICE)：返回 true
	builtins["trigger_error"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		msg := a[0].ToString()
		level := int64(1024) // E_USER_NOTICE
		if len(a) >= 2 {
			level = a[1].ToInt()
		}
		handled := e.handlePHPError(level, msg)
		// E_USER_ERROR 且未由处理器处理：PHP 语义为致命错误，中断脚本
		if level == 256 && !handled {
			return NewNull(), fmt.Errorf("PHP Fatal error: %s", msg)
		}
		return NewBool(true), nil
	}
	// error_get_last()：最近一次错误数组或 null
	builtins["error_get_last"] = func(e *Env, a []Value) (Value, error) {
		if e.lastErrLevel == 0 && e.lastErrMsg == "" {
			return NewNull(), nil
		}
		arr := NewArray()
		arr.ArraySet(NewString("type"), NewInt(e.lastErrLevel))
		arr.ArraySet(NewString("message"), NewString(e.lastErrMsg))
		arr.ArraySet(NewString("file"), NewString(e.scriptPath))
		arr.ArraySet(NewString("line"), NewInt(0))
		return arr, nil
	}
	// assert(mixed $assertion)：断言失败产生 E_WARNING（zend.assertions 开启时）
	builtins["assert"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		if a[0].ToBool() {
			return NewBool(true), nil
		}
		msg := "assertion failed"
		if len(a) >= 2 {
			msg = a[1].ToString()
		}
		_ = e.handlePHPError(2, msg) // E_WARNING
		return NewBool(false), nil
	}
}
