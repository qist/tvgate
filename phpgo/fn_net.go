package phpgo

import (
	"encoding/hex"
	"net"
	"net/url"
	"strconv"

	pgdns "github.com/qist/tvgate/dns"
)

func init() {
	// rawurlencode：RFC3986 编码（空格 -> %20）
	builtins["rawurlencode"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewString(""), nil
		}
		return NewString(url.PathEscape(a[0].ToString())), nil
	}
	// rawurldecode 对应（对称补充）
	builtins["rawurldecode"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewString(""), nil
		}
		s, err := url.PathUnescape(a[0].ToString())
		if err != nil {
			return NewString(a[0].ToString()), nil
		}
		return NewString(s), nil
	}
	// hex2bin：十六进制字符串 -> 二进制
	builtins["hex2bin"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewString(""), nil
		}
		b, err := hex.DecodeString(a[0].ToString())
		if err != nil {
			return NewBool(false), nil
		}
		return NewString(string(b)), nil
	}
	// bin2hex：二进制 -> 十六进制
	builtins["bin2hex"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewString(""), nil
		}
		return NewString(hex.EncodeToString([]byte(a[0].ToString()))), nil
	}
	// ip2long：IPv4 字符串 -> 有符号 32 位整数（PHP 语义）
	builtins["ip2long"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewBool(false), nil
		}
		ip := a[0].ToString()
		var b [4]byte
		var n int
		for i := 0; i < 4; i++ {
			if n >= len(ip) {
				return NewBool(false), nil
			}
			// 段
			start := n
			for n < len(ip) && ip[n] != '.' {
				n++
			}
			seg, err := strconv.Atoi(ip[start:n])
			if err != nil || seg > 255 || seg < 0 {
				return NewBool(false), nil
			}
			b[i] = byte(seg)
			if n < len(ip) {
				n++ // 跳过 '.'
			}
		}
		val := int64(b[0])<<24 | int64(b[1])<<16 | int64(b[2])<<8 | int64(b[3])
		if val > 2147483647 { // 转为负数以匹配 PHP 有符号行为
			val -= 4294967296
		}
		return NewInt(val), nil
	}
	// long2ip：整数 -> IPv4 字符串
	builtins["long2ip"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewString(""), nil
		}
		v := a[0].ToInt()
		v = v & 0xFFFFFFFF
		return NewString(strconv.FormatInt((v>>24)&0xFF, 10) + "." +
			strconv.FormatInt((v>>16)&0xFF, 10) + "." +
			strconv.FormatInt((v>>8)&0xFF, 10) + "." +
			strconv.FormatInt(v&0xFF, 10)), nil
	}
	// gethostbyname：主机名 -> IPv4 字符串（PHP 语义）
	// 已传入 IP 时原样返回；解析失败或无 IPv4 记录时返回原 hostname
	builtins["gethostbyname"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewString(""), nil
		}
		host := a[0].ToString()
		if net.ParseIP(host) != nil {
			return NewString(host), nil
		}
		ips, err := net.LookupIP(host)
		if err != nil || len(ips) == 0 {
			return NewString(host), nil
		}
		for _, ip := range ips {
			if v4 := ip.To4(); v4 != nil {
				return NewString(v4.String()), nil
			}
		}
		return NewString(host), nil
	}
	// gethostbynamel：主机名 -> IPv4 字符串数组（解析失败返回 false）
	builtins["gethostbynamel"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewBool(false), nil
		}
		host := a[0].ToString()
		if net.ParseIP(host) != nil {
			return NewBool(false), nil
		}
		ips, err := net.LookupIP(host)
		if err != nil || len(ips) == 0 {
			return NewBool(false), nil
		}
		arr := NewArray()
		n := 0
		for _, ip := range ips {
			if v4 := ip.To4(); v4 != nil {
				arr.ArraySet(NewInt(int64(n)), NewString(v4.String()))
				n++
			}
		}
		if n == 0 {
			return NewBool(false), nil
		}
		return arr, nil
	}
	// dns_get_record：DNS 记录查询（PHP 语义，支持 A/AAAA 类型）
	// $type 为 DNS_A | DNS_AAAA 位掩码（DNS_ANY 缺省，A/AAAA 都查）；
	// 返回记录数组，每条含 host/class/ttl/type + ip(A) 或 ipv6(AAAA)，ttl 为真实 TTL；
	// 解析失败返回 false；不支持的记录类型（NS/MX/TXT 等）返回空数组
	builtins["dns_get_record"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		host := a[0].ToString()
		typ := int64(268435456) // 缺省 DNS_ANY
		if len(a) >= 2 {
			typ = a[1].ToInt()
		}
		wantA := typ&1 != 0 || typ&268435456 != 0            // DNS_A / DNS_ANY
		wantAAAA := typ&134217728 != 0 || typ&268435456 != 0 // DNS_AAAA / DNS_ANY
		result := NewArray()
		n := 0
		addRec := func(ip string, ttl uint32, rtype, ipkey string) {
			rec := NewArray()
			rec.ArraySet(NewString("host"), NewString(host))
			rec.ArraySet(NewString("class"), NewString("IN"))
			rec.ArraySet(NewString("ttl"), NewInt(int64(ttl)))
			rec.ArraySet(NewString("type"), NewString(rtype))
			rec.ArraySet(NewString(ipkey), NewString(ip))
			result.ArraySet(NewInt(int64(n)), rec)
			n++
		}
		fetch := func(wantAAAA bool, rtype, ipkey string) bool {
			// 走项目 DNS 解析器（配置客户端 → 系统解析器），带真实 TTL
			if recs, err := pgdns.LookupIPWithTTL(host, wantAAAA); err == nil {
				for _, r := range recs {
					addRec(r.IP.String(), r.TTL, rtype, ipkey)
				}
				return true
			}
			// 解析失败兜底：net.LookupIP 拿 IP（ttl 记 0）
			if ips, err := net.LookupIP(host); err == nil {
				for _, ip := range ips {
					v4 := ip.To4()
					if wantAAAA {
						if v4 == nil {
							addRec(ip.String(), 0, rtype, ipkey)
						}
					} else if v4 != nil {
						addRec(v4.String(), 0, rtype, ipkey)
					}
				}
				return true
			}
			return false
		}
		okA, okAAAA := false, false
		if wantA {
			okA = fetch(false, "A", "ip")
		}
		if wantAAAA {
			okAAAA = fetch(true, "AAAA", "ipv6")
		}
		if n == 0 && !okA && !okAAAA {
			return NewBool(false), nil // 所有请求类型均解析失败
		}
		return result, nil
	}
	// utf8_encode：ISO-8859-1 -> UTF-8（PHP 历史语义）
	builtins["utf8_encode"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewString(""), nil
		}
		b := []byte(a[0].ToString())
		out := make([]byte, 0, len(b)*2)
		for _, c := range b {
			if c < 0x80 {
				out = append(out, c)
			} else {
				out = append(out, 0xC2|(c>>6), 0x80|(c&0x3F))
			}
		}
		return NewString(string(out)), nil
	}
}
