package phpgo

// curlOptIntToName 把 CURLOPT_* 整数值映射为字符串名（用于 curl_setopt/curl_exec 内部存储）
func curlOptIntToName(v int64) string {
	switch v {
	case 1:
		return "CURLOPT_URL"
	case 19913:
		return "CURLOPT_RETURNTRANSFER"
	case 47:
		return "CURLOPT_POST"
	case 10015:
		return "CURLOPT_POSTFIELDS"
	case 10023:
		return "CURLOPT_HTTPHEADER"
	case 13:
		return "CURLOPT_TIMEOUT"
	case 78:
		return "CURLOPT_CONNECTTIMEOUT"
	case 10018:
		return "CURLOPT_USERAGENT"
	case 52:
		return "CURLOPT_FOLLOWLOCATION"
	case 10036:
		return "CURLOPT_CUSTOMREQUEST"
	case 44:
		return "CURLOPT_NOBODY"
	case 10004:
		return "CURLOPT_PROXY"
	case 10100:
		return "CURLOPT_PROXYTYPE"
	case 64:
		return "CURLOPT_SSL_VERIFYPEER"
	case 81:
		return "CURLOPT_SSL_VERIFYHOST"
	case 10102:
		return "CURLOPT_ENCODING"
	case 10016:
		return "CURLOPT_REFERER"
	case 10022:
		return "CURLOPT_COOKIE"
	case 10031:
		return "CURLOPT_COOKIEFILE"
	case 10082:
		return "CURLOPT_COOKIEJAR"
	case 42:
		return "CURLOPT_HEADER"
	case 41:
		return "CURLOPT_VERBOSE"
	case 45:
		return "CURLOPT_FAILONERROR"
	case 75:
		return "CURLOPT_FORBID_REUSE"
	case 74:
		return "CURLOPT_FRESH_CONNECT"
	case 68:
		return "CURLOPT_MAXREDIRS"
	case 32:
		return "CURLOPT_SSLVERSION"
	case 10065:
		return "CURLOPT_CAINFO"
	case 10097:
		return "CURLOPT_CAPATH"
	case 10025:
		return "CURLOPT_SSLCERT"
	case 10026:
		return "CURLOPT_SSLKEY"
	case 80:
		return "CURLOPT_HTTPGET"
	case 3:
		return "CURLOPT_PORT"
	case 10001:
		return "CURLOPT_FILE"
	case 20011:
		return "CURLOPT_WRITEFUNCTION"
	case 20079:
		return "CURLOPT_HEADERFUNCTION"
	}
	return ""
}

// curlOptNameToInt 把字符串名映射为整数（反向查找）
func curlOptNameToInt(name string) int64 {
	for _, v := range []int64{1, 19913, 47, 10015, 10023, 13, 78, 10018, 52, 10036, 44, 10004, 10100, 64, 81, 10102, 10016, 10022, 10031, 10082, 42, 41, 45, 75, 74, 68, 32, 10065, 10097, 10025, 10026, 80, 3, 10001, 20011, 20079} {
		if curlOptIntToName(v) == name {
			return v
		}
	}
	return -1
}

// curlOptKey 返回 option 的标准化字符串 key（兼容整数和字符串两种形式）
func curlOptKey(v Value) string {
	s := v.ToString()
	// 如果是纯数字，映射为常量名
	if n, ok := tryParseInt64(s); ok {
		if name := curlOptIntToName(n); name != "" {
			return name
		}
	}
	return s
}

// tryParseInt64 尝试解析整数字符串
func tryParseInt64(s string) (int64, bool) {
	if len(s) == 0 {
		return 0, false
	}
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			// 可能是负数
			if len(s) > 1 && s[0] == '-' {
				continue
			}
			return 0, false
		}
		n = n*10 + int64(c-'0')
	}
	if s[0] == '-' {
		n = -n
	}
	return n, true
}
