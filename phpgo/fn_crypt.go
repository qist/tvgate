package phpgo

// crypt($string, $salt)。
//
// 支持：
//   - $1$…：FreeBSD MD5-crypt（与 glibc/PHP 兼容；用 golang.org/x/crypto/bcrypt 参考库的
//     算法族不同，此处按 MD5-crypt 规范实现并已用 glibc 向量校验）；
//   - $2a$/$2b$/$2y$…：bcrypt 需要固定盐复现，Go API 无法指定盐，故不做（建议改用
//     password_verify/password_hash）。
//   - 其它（含 DES）不受支持：返回 "*0"（与 PHP 在禁用对应算法时的标记一致）。
//
// 无盐时按 $1$ 生成随机 8 字符盐。

import (
	"crypto/md5"
	"math/rand"
	"strings"
)

const md5CryptAlphabet = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// md5CryptEncode24 输出 n 个 6-bit 字符（从低比特位开始，对齐 FreeBSD b64_from_24bit）
func md5CryptEncode24(b0, b1, b2 byte, n int) string {
	w := int(b2)<<16 | int(b1)<<8 | int(b0)
	var b strings.Builder
	for n > 0 {
		n--
		b.WriteByte(md5CryptAlphabet[w&0x3f])
		w >>= 6
	}
	return b.String()
}

// phpMD5Crypt 实现 MD5-crypt
func phpMD5Crypt(password, salt string) string {
	// 盐最多 8 字符，去掉尾随 $
	salt = strings.TrimSuffix(salt, "$")
	if len(salt) > 8 {
		salt = salt[:8]
	}
	magic := "$1$"

	h := md5.New()
	h.Write([]byte(password))
	h.Write([]byte(magic))
	h.Write([]byte(salt))
	mixin := md5.Sum(append(append([]byte(password), []byte(salt)...), []byte(password)...))

	// 交替追加 mixin
	cnt := len(password)
	for cnt > 0 {
		n := cnt
		if n > 16 {
			n = 16
		}
		h.Write(mixin[:n])
		cnt -= 16
	}
	// glibc「weird」步骤：先把 alt_result[0] 置 0；奇数位补 0 字节，偶数位补密码首字符
	mixin[0] = 0
	cnt = len(password)
	for cnt > 0 {
		if cnt&1 != 0 {
			h.Write([]byte{mixin[0]})
		} else {
			h.Write([]byte{password[0]})
		}
		cnt >>= 1
	}
	final := h.Sum(nil)

	for i := 0; i < 1000; i++ {
		h = md5.New()
		if i&1 != 0 {
			h.Write([]byte(password))
		} else {
			h.Write(final)
		}
		if i%3 != 0 {
			h.Write([]byte(salt))
		}
		if i%7 != 0 {
			h.Write([]byte(password))
		}
		if i&1 != 0 {
			h.Write(final)
		} else {
			h.Write([]byte(password))
		}
		final = h.Sum(nil)
	}

	var out strings.Builder
	out.WriteString(magic)
	out.WriteString(salt)
	out.WriteString("$")
	// __b64_from_24bit(&cp,&buflen, B2,B1,B0, n)：w = B2<<16|B1<<8|B0，
	// 从低位输出 n 个字符。glibc 的调用顺序：
	//   (alt[0],alt[6],alt[12])、(alt[1],alt[7],alt[13])、(alt[2],alt[8],alt[14])、
	//   (alt[3],alt[9],alt[15])、(alt[4],alt[10],alt[5])，最后 (0,0,alt[11],2)。
	// 我们的 md5CryptEncode24(b0,b1,b2) 的 w = b2<<16|b1<<8|b0，故实参按 (B0,B1,B2) 反传。
	out.WriteString(md5CryptEncode24(final[12], final[6], final[0], 4))
	out.WriteString(md5CryptEncode24(final[13], final[7], final[1], 4))
	out.WriteString(md5CryptEncode24(final[14], final[8], final[2], 4))
	out.WriteString(md5CryptEncode24(final[15], final[9], final[3], 4))
	out.WriteString(md5CryptEncode24(final[5], final[10], final[4], 4))
	out.WriteString(md5CryptEncode24(final[11], 0, 0, 2))
	return out.String()
}

func randomSalt(n int) string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}

func init() {
	builtins["crypt"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewString("*0"), nil
		}
		password := a[0].ToString()
		salt := ""
		if len(a) >= 2 {
			salt = a[1].ToString()
		}
		if salt == "" {
			salt = "$1$" + randomSalt(8)
		}
		if strings.HasPrefix(salt, "$1$") {
			parts := strings.SplitN(salt, "$", 4)
			s := ""
			if len(parts) >= 3 {
				s = parts[2]
			}
			if s == "" {
				s = randomSalt(8)
			}
			return NewString(phpMD5Crypt(password, s)), nil
		}
		// 不支持的算法（含 bcrypt/des/sha）：返回 "*0" 标记
		return NewString("*0"), nil
	}
}
