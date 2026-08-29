package phpgo

import (
	"sync"
)

// curl 扩展函数（不与 funcs.go 重复注册）
// curl_setopt_array 和 curl_getinfo 在 funcs.go 中注册

func init() {
	builtins["curl_errno"] = func(e *Env, a []Value) (Value, error) {
		return NewInt(0), nil
	}

	builtins["curl_reset"] = func(e *Env, a []Value) (Value, error) {
		return NewNull(), nil
	}

	// curl_multi_init：创建 multi 句柄（数组 + __multi 标记）
	builtins["curl_multi_init"] = func(e *Env, a []Value) (Value, error) {
		mh := NewArray()
		mh.ArraySet(NewString("__multi"), NewBool(true))
		return mh, nil
	}
	// curl_multi_add_handle：把单句柄加入 multi（句柄与原 $ch 共享数据，结果写回原变量）
	builtins["curl_multi_add_handle"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewInt(-1), nil // CURLM_BAD_HANDLE
		}
		mh := deref(a[0])
		if mh.Kind != KindArray || !mh.ArrayGet(NewString("__multi")).ToBool() {
			return NewInt(-1), nil
		}
		handles := mh.ArrayGet(NewString("__handles"))
		if handles.Kind != KindArray {
			handles = NewArray()
		}
		handles.ArraySet(NewInt(int64(len(handles.Keys))), deref(a[1]))
		mh.ArraySet(NewString("__handles"), handles)
		return NewInt(0), nil // CURLM_OK
	}
	// curl_multi_exec：并发执行全部已加入句柄（同步阻塞完成），$still_running 置 0
	// 标准用法 do { curl_multi_exec($mh,$running); } while($running>0) 一次循环即完成
	builtins["curl_multi_exec"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewInt(-1), nil
		}
		mh := deref(a[0])
		if mh.Kind != KindArray || !mh.ArrayGet(NewString("__multi")).ToBool() {
			return NewInt(-1), nil
		}
		if !mh.ArrayGet(NewString("__done")).ToBool() {
			handles := mh.ArrayGet(NewString("__handles"))
			if handles.Kind == KindArray && len(handles.Keys) > 0 {
				var wg sync.WaitGroup
				for _, k := range handles.Keys {
					h := handles.Arr[k]
					wg.Add(1)
					go func(h Value) {
						defer wg.Done()
						// 结果通过共享 map 写回脚本侧 $ch（供 curl_multi_getcontent/curl_getinfo 读取）
						_, _ = e.execCurlHandle(h)
					}(h)
				}
				wg.Wait()
			}
			mh.ArraySet(NewString("__done"), NewBool(true))
		}
		writeRef(e, a[1], NewInt(0)) // $still_running = 0
		return NewInt(0), nil
	}
	// curl_multi_getcontent：返回句柄的响应体
	builtins["curl_multi_getcontent"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray {
			return NewString(""), nil
		}
		return a[0].ArrayGet(NewString("__response")), nil
	}
	// curl_multi_info_read：exec 已同步完成，返回 false（无待读消息）
	builtins["curl_multi_info_read"] = func(e *Env, a []Value) (Value, error) {
		return NewBool(false), nil
	}
	// curl_multi_select：exec 同步完成，无等待中的句柄
	builtins["curl_multi_select"] = func(e *Env, a []Value) (Value, error) {
		return NewInt(0), nil
	}
	// curl_multi_remove_handle：exec 前移除句柄（同步模式下结果已整体计算，移除为幂等空操作）
	builtins["curl_multi_remove_handle"] = func(e *Env, a []Value) (Value, error) {
		return NewInt(0), nil
	}
	builtins["curl_multi_close"] = func(e *Env, a []Value) (Value, error) {
		return NewNull(), nil
	}
}
