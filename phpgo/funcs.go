package phpgo

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// BuiltinFunc 内置函数签名
type BuiltinFunc func(e *Env, args []Value) (Value, error)

var builtins = map[string]BuiltinFunc{}

func init() {
	builtins["define"] = func(e *Env, a []Value) (Value, error) {
		if len(a) >= 2 {
			e.consts[a[0].ToString()] = a[1]
		}
		return NewBool(true), nil
	}
	builtins["echo"] = func(e *Env, a []Value) (Value, error) {
		for _, v := range a {
			e.echoOut.WriteString(v.ToString())
		}
		return NewNull(), nil
	}
	builtins["array_key_exists"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		arr := a[1]
		return NewBool(arr.IsArrayKeyExists(a[0])), nil
	}
	builtins["array_keys"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 || a[0].Kind != KindArray {
			return NewArray(), nil
		}
		v := NewArray()
		for _, k := range a[0].Keys {
			v.ArraySet(NewInt(int64(len(v.Keys))), NewString(k))
		}
		return v, nil
	}
	builtins["count"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewInt(0), nil
		}
		if a[0].Kind == KindArray {
			return NewInt(int64(len(a[0].Keys))), nil
		}
		return NewInt(0), nil
	}
	builtins["strlen"] = func(e *Env, a []Value) (Value, error) {
		return NewInt(int64(len(a[0].ToString()))), nil
	}
	builtins["substr"] = func(e *Env, a []Value) (Value, error) {
		s := a[0].ToString()
		start := a[1].ToInt()
		if start < 0 {
			start = int64(len(s)) + start
		}
		if start < 0 || start > int64(len(s)) {
			return NewString(""), nil
		}
		end := int64(len(s))
		if len(a) >= 3 {
			l := a[2].ToInt()
			if l >= 0 {
				end = start + l
			} else {
				end = int64(len(s)) + l
			}
		}
		if end > int64(len(s)) {
			end = int64(len(s))
		}
		return NewString(s[start:end]), nil
	}
	builtins["strpos"] = func(e *Env, a []Value) (Value, error) {
		s := a[0].ToString()
		sub := a[1].ToString()
		idx := strings.Index(s, sub)
		if idx < 0 {
			return NewBool(false), nil
		}
		return NewInt(int64(idx)), nil
	}
	builtins["urlencode"] = func(e *Env, a []Value) (Value, error) {
		return NewString(url.QueryEscape(a[0].ToString())), nil
	}
	builtins["base64_encode"] = func(e *Env, a []Value) (Value, error) {
		return NewString(base64.StdEncoding.EncodeToString([]byte(a[0].ToString()))), nil
	}
	builtins["base64_decode"] = func(e *Env, a []Value) (Value, error) {
		b, err := base64.StdEncoding.DecodeString(a[0].ToString())
		if err != nil {
			return NewString(""), nil
		}
		return NewString(string(b)), nil
	}
	// json_encode/decode
	builtins["json_encode"] = func(e *Env, a []Value) (Value, error) {
		b, err := json.Marshal(phpToGo(a[0]))
		if err != nil {
			return NewString(""), err
		}
		return NewString(string(b)), nil
	}
	builtins["json_decode"] = func(e *Env, a []Value) (Value, error) {
		assoc := false
		if len(a) >= 2 {
			assoc = a[1].ToBool()
		}
		var raw interface{}
		if err := json.Unmarshal([]byte(a[0].ToString()), &raw); err != nil {
			return NewNull(), err
		}
		return goToPHP(raw, assoc), nil
	}
	// openssl
	builtins["openssl_encrypt"] = func(e *Env, a []Value) (Value, error) {
		return opensslCipher(a, true)
	}
	builtins["openssl_decrypt"] = func(e *Env, a []Value) (Value, error) {
		return opensslCipher(a, false)
	}
	// header（捕获到 env.headers）
	builtins["header"] = func(e *Env, a []Value) (Value, error) {
		if len(a) > 0 {
			e.headers = append(e.headers, a[0].ToString())
		}
		return NewNull(), nil
	}
	builtins["http_response_code"] = func(e *Env, a []Value) (Value, error) {
		return NewInt(200), nil
	}
	// curl 系列
	builtins["curl_init"] = func(e *Env, a []Value) (Value, error) {
		// 返回一个句柄（用 map 存选项）
		h := NewArray()
		if len(a) > 0 {
			h.ArraySet(NewString("url"), a[0])
		}
		return h, nil
	}
	builtins["curl_setopt"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 3 {
			return NewBool(false), nil
		}
		h := a[0]
		optName := a[1].ToString() // 如 "CURLOPT_URL"
		val := a[2]
		h.ArraySet(NewString(optName), val)
		return NewBool(true), nil
	}
	builtins["curl_setopt_array"] = func(e *Env, a []Value) (Value, error) {
		return NewBool(true), nil
	}
	builtins["curl_exec"] = func(e *Env, a []Value) (Value, error) {
		return e.curlExec(a[0])
	}
	builtins["curl_error"] = func(e *Env, a []Value) (Value, error) {
		return NewString(""), nil
	}
	builtins["curl_getinfo"] = func(e *Env, a []Value) (Value, error) {
		return NewArray(), nil
	}
	builtins["curl_close"] = func(e *Env, a []Value) (Value, error) {
		return NewNull(), nil
	}
	// file_get_contents（HTTP 走 proxy，本地文件直接读，php://input 读请求体）
	builtins["file_get_contents"] = fileGetContents
	// PCRE 正则（Go RE2 子集）
	builtins["preg_match"] = phpPregMatch
	builtins["preg_match_all"] = phpPregMatchAll
	builtins["preg_replace"] = phpPregReplace
}

// ---------------------------------------------------------------------------
// openssl AES-CBC 实现（对齐 PHP openssl_encrypt/decrypt）
// PHP 默认使用零填充（OPENSSL_ZERO_PADDING 配合 RAW_DATA）或 PKCS7。
// 4gtv.php 使用 OPENSSL_RAW_DATA（=1），PHP 默认 padding = PKCS7。
// ---------------------------------------------------------------------------

func opensslCipher(a []Value, encrypt bool) (Value, error) {
	if len(a) < 3 {
		return NewString(""), fmt.Errorf("openssl: 参数不足")
	}
	data := []byte(a[0].ToString())
	method := a[1].ToString() // "AES-256-CBC"
	key := []byte(a[2].ToString())
	var iv []byte
	if len(a) >= 5 {
		iv = []byte(a[4].ToString())
	}
	// 解析方法
	var blockSize int
	switch method {
	case "AES-256-CBC":
		blockSize = 16
	case "AES-128-CBC":
		blockSize = 16
	default:
		return NewString(""), fmt.Errorf("openssl: 不支持的方法 %s", method)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return NewString(""), err
	}
	mode := cipher.NewCBCDecrypter(block, iv) // 占位，下面按方向选
	_ = mode
	if encrypt {
		// PKCS7 填充
		pad := blockSize - len(data)%blockSize
		padtext := make([]byte, pad)
		for i := range padtext {
			padtext[i] = byte(pad)
		}
		data = append(data, padtext...)
		enc := make([]byte, len(data))
		cipher.NewCBCEncrypter(block, iv).CryptBlocks(enc, data)
		// OPENSSL_RAW_DATA(=1) 返回原始字节；否则 base64（由调用方处理）
		raw := false
		if len(a) >= 4 {
			raw = a[3].ToInt() == 1
		}
		if raw {
			return NewString(string(enc)), nil
		}
		return NewString(base64.StdEncoding.EncodeToString(enc)), nil
	}
	// decrypt
	if len(data)%blockSize != 0 {
		return NewString(""), fmt.Errorf("openssl: 数据长度不是块大小整数倍")
	}
	dec := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(dec, data)
	// 去除 PKCS7 填充
	if len(dec) > 0 {
		pad := int(dec[len(dec)-1])
		if pad > 0 && pad <= blockSize && pad <= len(dec) {
			dec = dec[:len(dec)-pad]
		}
	}
	raw := false
	if len(a) >= 4 {
		raw = a[3].ToInt() == 1
	}
	if raw {
		return NewString(string(dec)), nil
	}
	return NewString(base64.StdEncoding.EncodeToString(dec)), nil
}

// ---------------------------------------------------------------------------
// curl 执行：统一路由到 Env.proxy（Go 实现 HTTP + 代理）
// ---------------------------------------------------------------------------

func (e *Env) curlExec(h Value) (Value, error) {
	opts := &CurlOptions{Timeout: 30}
	if h.Kind == KindArray {
		// 取出 URL
		urlVal := h.Arr["url"]
		if urlVal.Kind == KindNull {
			urlVal = h.Arr["CURLOPT_URL"]
		}
		finalURL := urlVal.ToString()
		// 代理
		if pr, ok := h.Arr["CURLOPT_PROXY"]; ok {
			opts.Proxy = pr.ToString()
		}
		if pt, ok := h.Arr["CURLOPT_PROXYTYPE"]; ok {
			opts.ProxyType = pt.ToString()
		}
		if hd, ok := h.Arr["CURLOPT_HTTPHEADER"]; ok {
			if hd.Kind == KindArray {
				for _, k := range hd.Keys {
					opts.Headers = append(opts.Headers, hd.Arr[k].ToString())
				}
			}
		}
		if pd, ok := h.Arr["CURLOPT_POSTFIELDS"]; ok {
			opts.PostData = pd.ToString()
		}
		if to, ok := h.Arr["CURLOPT_TIMEOUT"]; ok {
			opts.Timeout = int(to.ToInt())
		}
		if ct, ok := h.Arr["CURLOPT_CONNECTTIMEOUT"]; ok {
			_ = ct // 简化：用同一 timeout
		}
		if ua, ok := h.Arr["CURLOPT_USERAGENT"]; ok {
			opts.UserAgent = ua.ToString()
		}
		// FOLLOWLOCATION
		if fl, ok := h.Arr["CURLOPT_FOLLOWLOCATION"]; ok {
			opts.FollowRedirect = fl.ToBool()
		}
		method := "GET"
		if _, ok := h.Arr["CURLOPT_POST"]; ok {
			method = "POST"
		}
		// CURLOPT_CUSTOMREQUEST
		if cr, ok := h.Arr["CURLOPT_CUSTOMREQUEST"]; ok {
			method = cr.ToString()
		}
		// CURLOPT_NOBODY -> HEAD
		if nb, ok := h.Arr["CURLOPT_NOBODY"]; ok && nb.ToBool() {
			method = "HEAD"
		}
		if e.proxy == nil {
			return NewString(""), fmt.Errorf("curl: 未配置 proxy 后端")
		}
		body, err := e.proxy(method, finalURL, opts)
		if err != nil {
			return NewString("Error: " + err.Error()), nil
		}
		// 记录 info
		h.ArraySet(NewString("__http_code"), NewInt(200))
		h.ArraySet(NewString("__effective_url"), NewString(finalURL))
		h.ArraySet(NewString("__content_type"), NewString("text/html; charset=utf-8"))
		h.ArraySet(NewString("__response"), NewString(body))

		// CURLOPT_RETURNTRANSFER: true 时返回内容，false 时直接输出
		returnRaw := true
		if rt, ok := h.Arr["CURLOPT_RETURNTRANSFER"]; ok {
			returnRaw = rt.ToBool()
		}
		if !returnRaw {
			e.writeOutput(body)
			return NewBool(true), nil
		}
		return NewString(body), nil
	}
	return NewString(""), nil
}


// ---------------------------------------------------------------------------
// JSON 互转
// ---------------------------------------------------------------------------

// phpToGo 把 PHP Value 转 Go 值用于 json.Marshal
func phpToGo(v Value) interface{} {
	switch v.Kind {
	case KindNull:
		return nil
	case KindBool:
		return v.Bool
	case KindInt:
		return v.Int
	case KindFloat:
		return v.Float
	case KindString:
		return v.Str
	case KindArray:
		// 判断是索引还是关联
		if len(v.Keys) > 0 {
			allInt := true
			for _, k := range v.Keys {
				if _, err := fmt.Sscanf(k, "%d", new(int64)); err != nil {
					allInt = false
					break
				}
			}
			if allInt {
				arr := make([]interface{}, 0, len(v.Keys))
				for _, k := range v.Keys {
					idx, _ := fmt.Sscanf(k, "%d", new(int64))
					_ = idx
				}
				// 按 key 数值排序
				keys := append([]string{}, v.Keys...)
				sortStrings(keys)
				for _, k := range keys {
					arr = append(arr, phpToGo(v.Arr[k]))
				}
				return arr
			}
			m := map[string]interface{}{}
			for _, k := range v.Keys {
				m[k] = phpToGo(v.Arr[k])
			}
			return m
		}
		return []interface{}{}
	}
	return nil
}

// goToPHP 把 Go 解码值转 PHP Value（assoc=true 时对象转关联数组）
func goToPHP(v interface{}, assoc bool) Value {
	switch t := v.(type) {
	case nil:
		return NewNull()
	case bool:
		return NewBool(t)
	case float64:
		if t == float64(int64(t)) {
			return NewInt(int64(t))
		}
		return NewFloat(t)
	case string:
		return NewString(t)
	case []interface{}:
		arr := NewArray()
		for i, el := range t {
			arr.ArraySet(NewInt(int64(i)), goToPHP(el, assoc))
		}
		return arr
	case map[string]interface{}:
		arr := NewArray()
		for k, el := range t {
			arr.ArraySet(NewString(k), goToPHP(el, assoc))
		}
		return arr
	}
	return NewNull()
}
