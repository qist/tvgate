package phpgo

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"strings"
)

func init() {
	// openssl_pkey_get_public：解析 PEM 公钥，返回资源（存 *rsa.PublicKey）
	builtins["openssl_pkey_get_public"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewBool(false), nil
		}
		pub, err := parsePublicKey(a[0].ToString())
		if err != nil {
			return NewBool(false), nil
		}
		return NewResource(pub), nil
	}
	// openssl_public_encrypt：用公钥加密（默认 OPENSSL_PKCS1_PADDING）
	builtins["openssl_public_encrypt"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 3 {
			return NewBool(false), nil
		}
		data := a[0].ToString()
		res := a[1]
		key := resToPublicKey(e, a[2])
		if key == nil {
			return NewBool(false), nil
		}
		// padding 默认 PKCS1
		padding := 1
		if len(a) >= 4 {
			padding = int(a[3].ToInt())
		}
		var out []byte
		var err error
		switch padding {
		case 1: // OPENSSL_PKCS1_PADDING
			out, err = rsa.EncryptPKCS1v15(rand.Reader, key, []byte(data))
		case 4: // OPENSSL_PKCS1_OAEP_PADDING
			out, err = rsa.EncryptOAEP(sha1.New(), rand.Reader, key, []byte(data), nil)
		case 6: // OPENSSL_PKCS1_OAEP_PADDING + sha256
			out, err = rsa.EncryptOAEP(sha256.New(), rand.Reader, key, []byte(data), nil)
		default:
			out, err = rsa.EncryptPKCS1v15(rand.Reader, key, []byte(data))
		}
		if err != nil {
			return NewBool(false), nil
		}
		// PHP 把加密结果写入第 2 个参数（引用）
		writeRef(e, res, NewString(string(out)))
		return NewBool(true), nil
	}
	// openssl_public_decrypt：用公钥解密（少见，但也支持）
	builtins["openssl_public_decrypt"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 3 {
			return NewBool(false), nil
		}
		data := []byte(a[0].ToString())
		res := a[1]
		key := resToPublicKey(e, a[2])
		if key == nil {
			return NewBool(false), nil
		}
		out, err := rsa.DecryptPKCS1v15(rand.Reader, &rsa.PrivateKey{PublicKey: *key}, data)
		if err != nil {
			return NewBool(false), nil
		}
		writeRef(e, res, NewString(string(out)))
		return NewBool(true), nil
	}
}

// parsePublicKey 从 PEM 字符串解析 RSA 公钥
func parsePublicKey(pemStr string) (*rsa.PublicKey, error) {
	pemStr = strings.TrimSpace(pemStr)
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("invalid pem")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		// 尝试 PKCS1
		pk, e2 := x509.ParsePKCS1PublicKey(block.Bytes)
		if e2 != nil {
			return nil, err
		}
		return pk, nil
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("not rsa public key")
	}
	return rsaPub, nil
}

// resToPublicKey 从资源值中取出 *rsa.PublicKey
func resToPublicKey(e *Env, v Value) *rsa.PublicKey {
	if v.Kind == KindResource {
		if k, ok := v.Resource.(*rsa.PublicKey); ok {
			return k
		}
	}
	return nil
}
