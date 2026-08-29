package phpgo

import (
	"crypto/tls"
	"os"
	"strings"
)

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
	case 113:
		return "CURLOPT_IPRESOLVE"
	}
	return ""
}

// curlOptNameToInt 把字符串名映射为整数（反向查找）
func curlOptNameToInt(name string) int64 {
	for _, v := range []int64{1, 19913, 47, 10015, 10023, 13, 78, 10018, 52, 10036, 44, 10004, 10100, 64, 81, 10102, 10016, 10022, 10031, 10082, 42, 41, 45, 75, 74, 68, 32, 10065, 10097, 10025, 10026, 80, 3, 10001, 20011, 20079, 113} {
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

// curlTLSVersion 把 CURL_SSLVERSION_* 常量值映射为 Go tls 最低版本。
// 0=默认 1=TLSv1 4=TLSv1.0 5=TLSv1.1 6=TLSv1.2 7=TLSv1.3（2/3 为已废弃的 SSLv2/v3）。
func curlTLSVersion(v int) uint16 {
	switch v {
	case 4: // CURL_SSLVERSION_TLSv1_0
		return tls.VersionTLS10
	case 5: // CURL_SSLVERSION_TLSv1_1
		return tls.VersionTLS11
	case 6: // CURL_SSLVERSION_TLSv1_2
		return tls.VersionTLS12
	case 7: // CURL_SSLVERSION_TLSv1_3
		return tls.VersionTLS13
	case 1: // CURL_SSLVERSION_TLSv1
		return tls.VersionTLS12
	}
	return 0
}

// readCookieFile 从 Cookie 文件读取 name=value 对，返回可直接用于 Cookie 头的字符串。
// 兼容两种格式：header 行（"name=value; ..."）与 Netscape cookies.txt。
func readCookieFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var pairs []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Netscape 格式：# domain \t flag \t path \t secure \t expiry \t name \t value
		fields := strings.Split(line, "\t")
		if len(fields) >= 7 {
			name := strings.TrimSpace(fields[len(fields)-2])
			val := strings.TrimSpace(fields[len(fields)-1])
			if name != "" {
				pairs = append(pairs, name+"="+val)
			}
			continue
		}
		// header 格式：name=value; ...
		if i := strings.IndexByte(line, '='); i > 0 {
			if j := strings.IndexByte(line, ';'); j > 0 {
				line = line[:j]
			}
			pairs = append(pairs, strings.TrimSpace(line))
		}
	}
	return strings.Join(pairs, "; "), nil
}

// writeCookieJar 把响应头中的 Set-Cookie 按 Netscape cookies.txt 格式写入文件。
func writeCookieJar(path string, headers []string) {
	var lines []string
	lines = append(lines, "# Netscape HTTP Cookie File")
	for _, h := range headers {
		i := strings.IndexByte(h, ':')
		if i <= 0 || !strings.EqualFold(strings.TrimSpace(h[:i]), "Set-Cookie") {
			continue
		}
		cookie := strings.TrimSpace(h[i+1:])
		name, cval, domain, pathv, secure, expires := "", "", "", "/", "FALSE", "0"
		parts := strings.Split(cookie, ";")
		for i, p := range parts {
			p = strings.TrimSpace(p)
			if i == 0 {
				if eq := strings.IndexByte(p, '='); eq > 0 {
					name = p[:eq]
					cval = p[eq+1:]
				}
				continue
			}
			low := strings.ToLower(p)
			switch {
			case strings.HasPrefix(low, "path="):
				pathv = p[5:]
			case strings.HasPrefix(low, "domain="):
				domain = strings.TrimPrefix(p[7:], ".")
			case strings.HasPrefix(low, "secure"):
				secure = "TRUE"
			case strings.HasPrefix(low, "expires="):
				expires = p[8:]
			}
		}
		if name == "" {
			continue
		}
		if domain == "" {
			domain = "localhost"
		}
		lines = append(lines, strings.Join([]string{domain, "FALSE", pathv, secure, expires, name, cval}, "\t"))
	}
	_ = os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}
