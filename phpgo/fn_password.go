package phpgo

// password_hash / password_verify / password_needs_rehash。
// 使用 golang.org/x/crypto/bcrypt（已随 quic-go 引入，模块缓存可用）。
// PHP 默认（PASSWORD_DEFAULT）即 bcrypt，输出前缀 $2y$；Go bcrypt 生成 $2a$，
// 输出前统一改写为 $2y$，verify 时反向兼容三种前缀。
// Argon2i/Argon2id：phpgo 不内嵌 argon2，返回 false（与 PHP 未编译 argon2 时行为一致）。

import (
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

func init() {
	builtins["password_hash"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		password := a[0].ToString()
		algo := int64(1) // PASSWORD_DEFAULT / PASSWORD_BCRYPT
		if len(a) >= 2 {
			algo = a[1].ToInt()
		}
		cost := int64(10)
		if len(a) >= 3 && a[2].Kind == KindArray {
			if c, ok := a[2].Arr["cost"]; ok {
				cost = c.ToInt()
			}
		}
		switch algo {
		case 1: // PASSWORD_BCRYPT
			if cost < 4 {
				cost = 4
			}
			if cost > 31 {
				cost = 31
			}
			hash, err := bcrypt.GenerateFromPassword([]byte(password), int(cost))
			if err != nil {
				return NewBool(false), nil
			}
			// PHP 哈希统一 $2y$ 前缀
			return NewString(strings.Replace(string(hash), "$2a$", "$2y$", 1)), nil
		default:
			// PASSWORD_ARGON2I / ARGON2ID 等：运行时未编译，返回 false
			return NewBool(false), nil
		}
	}
	builtins["password_verify"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		hash := a[1].ToString()
		if hash == "" || !strings.HasPrefix(hash, "$2") {
			return NewBool(false), nil
		}
		h := strings.Replace(hash, "$2y$", "$2a$", 1)
		if bcrypt.CompareHashAndPassword([]byte(h), []byte(a[0].ToString())) == nil {
			return NewBool(true), nil
		}
		return NewBool(false), nil
	}
	builtins["password_needs_rehash"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		hash := a[0].ToString()
		algo := a[1].ToInt()
		if algo != 1 {
			return NewBool(true), nil // 只支持 bcrypt：请求其它算法一律视为需重哈希
		}
		cost := int64(10)
		if len(a) >= 3 && a[2].Kind == KindArray {
			if c, ok := a[2].Arr["cost"]; ok {
				cost = c.ToInt()
			}
		}
		// 解析已存哈希的 cost：$2y$10$...
		parts := strings.Split(hash, "$")
		if len(parts) < 4 || !strings.HasPrefix(parts[1], "2") {
			return NewBool(true), nil
		}
		var cur int64
		if n, err := strconv.ParseInt(parts[2], 10, 64); err == nil {
			cur = n
		} else {
			return NewBool(true), nil
		}
		return NewBool(cur != cost), nil
	}
}
