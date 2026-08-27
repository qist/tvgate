package phpgo

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
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
		goVal := phpToGo(a[0])
		// 默认行为：json.Marshal
		b, err := json.Marshal(goVal)
		if err != nil {
			return NewString(""), err
		}
		s := string(b)
		// 处理 JSON flags
		if len(a) >= 2 {
			flags := a[1].ToInt()
			if flags&256 != 0 { // JSON_UNESCAPED_UNICODE
				// Go 的 json.Marshal 默认会转义非 ASCII，需要反转义
				s = unescapeUnicode(s)
			}
			if flags&64 != 0 { // JSON_UNESCAPED_SLASHES
				s = strings.ReplaceAll(s, "\\/", "/")
			}
			if flags&128 != 0 { // JSON_PRETTY_PRINT
				var buf bytes.Buffer
				if err := json.Indent(&buf, b, "  ", "  "); err == nil {
					s = buf.String()
				}
			}
		}
		return NewString(s), nil
	}
	builtins["json_decode"] = func(e *Env, a []Value) (Value, error) {
		assoc := false
		if len(a) >= 2 {
			assoc = a[1].ToBool()
		}
		s := a[0].ToString()
		if s == "" {
			return NewNull(), nil
		}
		var raw interface{}
		if err := json.Unmarshal([]byte(s), &raw); err != nil {
			return NewNull(), nil
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
			h := a[0].ToString()
			// 显式状态码：header("HTTP/1.1 404 Not Found") 或
			// header("HTTP/1.0 301 Moved Permanently")。解析出数字状态码，
			// 不放入普通 header 列表（避免被当作响应头写出）。
			if code, ok := parseHTTPStatusHeader(h); ok {
				e.statusCode = code
				e.statusCodeSet = true
				return NewNull(), nil
			}
			e.headers = append(e.headers, h)
		}
		return NewNull(), nil
	}
	builtins["http_response_code"] = func(e *Env, a []Value) (Value, error) {
		if len(a) >= 1 {
			e.statusCode = int(a[0].ToInt())
			e.statusCodeSet = true
		}
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
		h := deref(a[0])
		optName := curlOptKey(a[1]) // 兼容整数常量和字符串
		val := a[2]
		h.ArraySet(NewString(optName), val)
		return NewBool(true), nil
	}
	builtins["curl_setopt_array"] = func(e *Env, a []Value) (Value, error) {
		h := deref(a[0])
		arr := deref(a[1])
		if len(a) < 2 || h.Kind != KindArray || arr.Kind != KindArray {
			return NewBool(false), nil
		}
		for _, k := range arr.Keys {
			// k 是原始 key（整数常量值转成的字符串），需要映射
			optName := k
			if n, ok := tryParseInt64(k); ok {
				if name := curlOptIntToName(n); name != "" {
				optName = name
			}
			}
			h.ArraySet(NewString(optName), arr.Arr[k])
		}
		return NewBool(true), nil
	}
	builtins["curl_exec"] = func(e *Env, a []Value) (Value, error) {
		return e.curlExec(deref(a[0]))
	}
	builtins["curl_error"] = func(e *Env, a []Value) (Value, error) {
		h := deref(a[0])
		if len(a) < 1 || h.Kind != KindArray {
			return NewString(""), nil
		}
		return h.ArrayGet(NewString("__error")), nil
	}
	builtins["curl_getinfo"] = func(e *Env, a []Value) (Value, error) {
		h := deref(a[0])
		if len(a) < 1 || h.Kind != KindArray {
			return NewArray(), nil
		}
		info := NewArray()
		info.ArraySet(NewString("http_code"), h.ArrayGet(NewString("__http_code")))
		info.ArraySet(NewString("effective_url"), h.ArrayGet(NewString("__effective_url")))
		info.ArraySet(NewString("content_type"), h.ArrayGet(NewString("__content_type")))
		info.ArraySet(NewString("redirect_url"), h.ArrayGet(NewString("__redirect_url")))
		// 单一选项模式: curl_getinfo($ch, CURLINFO_HTTP_CODE)
		if len(a) >= 2 {
			opt := a[1].ToString()
			switch opt {
			case "2097154": // CURLINFO_HTTP_CODE / CURLINFO_RESPONSE_CODE
				return info.ArrayGet(NewString("http_code")), nil
			case "1048577": // CURLINFO_EFFECTIVE_URL
				return info.ArrayGet(NewString("effective_url")), nil
			case "1048593": // CURLINFO_CONTENT_TYPE
				return info.ArrayGet(NewString("content_type")), nil
			case "3145744": // CURLINFO_REDIRECT_URL
				return info.ArrayGet(NewString("redirect_url")), nil
			}
			return info.ArrayGet(a[1]), nil
		}
		return info, nil
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

// unescapeUnicode 将 Go json.Marshal 产生的 \uXXXX 转义还原为 UTF-8 字符
func unescapeUnicode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if i+5 < len(s) && s[i] == '\\' && s[i+1] == 'u' {
			var code int
			fmt.Sscanf(s[i+2:i+6], "%x", &code)
			b.WriteRune(rune(code))
			i += 6
		} else {
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}

// parseHTTPStatusHeader 解析 header("HTTP/1.1 404 Not Found") 之类的显式状态码行。
// 命中返回 (code, true)，否则 (0, false)。命中时不作为普通响应头写出。
func parseHTTPStatusHeader(h string) (int, bool) {
	h = strings.TrimSpace(h)
	up := strings.ToUpper(h)
	if !strings.HasPrefix(up, "HTTP/") {
		return 0, false
	}
	// HTTP/1.1 404 ... 或 HTTP/1.1 404
	fields := strings.Fields(h)
	if len(fields) < 2 {
		return 0, false
	}
	// 第二段应为数字状态码
	var code int
	if _, err := fmt.Sscanf(fields[1], "%d", &code); err != nil || code < 100 || code > 599 {
		return 0, false
	}
	return code, true
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
	method := a[1].ToString() // "AES-256-CBC" / "aes-256-gcm"
	key := []byte(a[2].ToString())
	var iv []byte
	if len(a) >= 5 {
		iv = []byte(a[4].ToString())
	}
	// 解析方法
	switch method {
	case "AES-256-CBC", "AES-128-CBC":
		return opensslCBC(a, data, key, iv, encrypt)
	case "AES-256-ECB", "AES-128-ECB", "aes-128-ecb", "aes-256-ecb":
		return opensslAESECB(a, data, key, encrypt)
	case "aes-256-gcm", "AES-256-GCM", "aes-128-gcm", "AES-128-GCM":
		return opensslGCM(a, data, key, iv, encrypt)
	case "des-ede3", "DES-EDE3", "des-ede3-ecb", "DES-EDE3-ECB":
		return opensslTripleDESECB(a, data, key, encrypt)
	case "des-ede3-cbc", "DES-EDE3-CBC":
		return opensslTripleDESCBC(a, data, key, iv, encrypt)
	default:
		return NewString(""), fmt.Errorf("openssl: 不支持的方法 %s", method)
	}
}

// opensslCBC 实现 AES-CBC 加解密
func opensslCBC(a []Value, data, key, iv []byte, encrypt bool) (Value, error) {
	blockSize := 16
	block, err := aes.NewCipher(key)
	if err != nil {
		return NewString(""), err
	}
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

// opensslGCM 实现 AES-GCM 加解密
// PHP: openssl_encrypt($data, 'aes-256-gcm', $key, OPENSSL_RAW_DATA, $iv, $tag, $aad)
//      openssl_decrypt($data, 'aes-256-gcm', $key, OPENSSL_RAW_DATA, $iv, $tag, $aad)
func opensslGCM(a []Value, data, key, iv []byte, encrypt bool) (Value, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return NewString(""), err
	}
	// GCM 标准 nonce 是 12 字节，但 PHP/OpenSSL 允许任意长度 IV。
	// 使用 NewGCMWithNonceSize 支持 IV 长度。
	nonceSize := len(iv)
	if nonceSize == 0 {
		nonceSize = 12 // 默认
	}
	gcm, err := cipher.NewGCMWithNonceSize(block, nonceSize)
	if err != nil {
		return NewString(""), err
	}
	if len(iv) == 0 {
		iv = make([]byte, gcm.NonceSize())
	}
	raw := false
	if len(a) >= 4 {
		raw = a[3].ToInt() == 1
	}
	// 第6个参数是 tag，第7个参数是 aad
	var tag []byte
	var aad []byte
	if len(a) >= 6 {
		tag = []byte(a[5].ToString())
	}
	if len(a) >= 7 {
		aad = []byte(a[6].ToString())
	}
	if encrypt {
		enc := gcm.Seal(nil, iv, data, aad)
		// GCM 返回: ciphertext + tag（tag 是最后 gcm.Overhead() 字节）
		tagLen := gcm.Overhead()
		ct := enc[:len(enc)-tagLen]
		tagBytes := enc[len(enc)-tagLen:]
		_ = tagBytes // PHP 中 $tag 是引用参数，简化不写回
		if raw {
			return NewString(string(ct)), nil
		}
		return NewString(base64.StdEncoding.EncodeToString(ct)), nil
	}
	// decrypt
	// GCM: 需要把 ciphertext + tag 拼接起来
	ct := data
	if len(tag) > 0 {
		ct = append(ct, tag...)
	}
	dec, err := gcm.Open(nil, iv, ct, aad)
	if err != nil {
		return NewBool(false), fmt.Errorf("openssl: GCM 解密失败: %v", err)
	}
	if raw {
		return NewString(string(dec)), nil
	}
	return NewString(base64.StdEncoding.EncodeToString(dec)), nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// deref 解引用 KindRef 为实际值（用于内置函数接收 VarExpr 参数时）
func deref(v Value) Value {
	if v.Kind == KindRef && v.RefVal != nil {
		return *v.RefVal
	}
	return v
}

// curl 执行：统一路由到 Env.proxy（Go 实现 HTTP + 代理）
// ---------------------------------------------------------------------------

func (e *Env) curlExec(h Value) (Value, error) {
	h = deref(h)
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
			opts.HasPostData = true
		}
		if to, ok := h.Arr["CURLOPT_TIMEOUT"]; ok {
			// PHP 中 timeout 可以是浮点数（如 0.1 秒），用 ToFloat 而非 ToInt
			timeoutSec := to.ToFloat()
			if timeoutSec > 0 {
				opts.TimeoutFloat = timeoutSec
			} else {
				opts.Timeout = 30
			}
		} else {
			opts.Timeout = 30
		}
		if ct, ok := h.Arr["CURLOPT_CONNECTTIMEOUT"]; ok {
			ctv := ct.ToFloat()
			if ctv > 0 {
				opts.ConnectTimeoutFloat = ctv
			}
		}
		if ua, ok := h.Arr["CURLOPT_USERAGENT"]; ok {
			opts.UserAgent = ua.ToString()
		}
		// FOLLOWLOCATION
		if fl, ok := h.Arr["CURLOPT_FOLLOWLOCATION"]; ok {
			opts.FollowRedirect = fl.ToBool()
		}
		// SSL VERIFYPEER
		if ssl, ok := h.Arr["CURLOPT_SSL_VERIFYPEER"]; ok {
			opts.SkipSSL = !ssl.ToBool()
		}
		method := "GET"
		if _, ok := h.Arr["CURLOPT_POST"]; ok {
			method = "POST"
		}
		// CURLOPT_POSTFIELDS 有值时自动转为 POST（对齐 PHP curl 行为）
		if pd, ok := h.Arr["CURLOPT_POSTFIELDS"]; ok && pd.ToString() != "" {
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
			return NewBool(false), nil
		}
		result, err := e.proxy(method, finalURL, opts)
		if err != nil {
			// 记录错误信息到 handle，供 curl_error 使用
			h.ArraySet(NewString("__error"), NewString(err.Error()))
			return NewBool(false), nil
		}
		// 记录 info（真实 HTTP 状态码和重定向 URL）
		httpCode := 200
		if result.StatusCode > 0 {
			httpCode = result.StatusCode
		}
		h.ArraySet(NewString("__http_code"), NewInt(int64(httpCode)))
		// effective_url：优先使用跟随重定向后的最终 URL
		effURL := finalURL
		if result.EffectiveURL != "" {
			effURL = result.EffectiveURL
		}
		h.ArraySet(NewString("__effective_url"), NewString(effURL))
		h.ArraySet(NewString("__content_type"), NewString(result.ContentType))
		h.ArraySet(NewString("__redirect_url"), NewString(result.Location))
		h.ArraySet(NewString("__response"), NewString(result.Body))

		// CURLOPT_RETURNTRANSFER: true 时返回内容，false 时直接输出
		returnRaw := true
		if rt, ok := h.Arr["CURLOPT_RETURNTRANSFER"]; ok {
			returnRaw = rt.ToBool()
		}
		if !returnRaw {
			e.writeOutput(result.Body)
			return NewBool(true), nil
		}
		return NewString(result.Body), nil
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
		// PHP json_encode 规则：只有当 key 恰好是 0,1,2,...,N-1 的连续整数时，
		// 才输出 JSON 数组 []；否则输出 JSON 对象 {}。
		// phpgo 中所有 key 都以字符串存储，所以需要检查 keys 是否依次为 "0","1",...
		if len(v.Keys) > 0 {
			isSequential := true
			for i, k := range v.Keys {
				if k != strconv.FormatInt(int64(i), 10) {
					isSequential = false
					break
				}
			}
			if isSequential {
				arr := make([]interface{}, 0, len(v.Keys))
				for _, k := range v.Keys {
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
