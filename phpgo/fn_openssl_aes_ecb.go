package phpgo

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"
)

// opensslAESECB 实现 AES ECB 模式加解密
// PHP: openssl_encrypt($data, 'AES-128-ECB', $key, OPENSSL_RAW_DATA | OPENSSL_ZERO_PADDING)
// 当使用 OPENSSL_ZERO_PADDING 时，PHP 不做 PKCS7 填充，要求输入数据已是块大小整数倍。
// 当不使用 OPENSSL_ZERO_PADDING（仅 OPENSSL_RAW_DATA）时，PHP 使用 PKCS7 填充。
// newgitv.php 的 _enc 函数自己做了 PKCS7 填充并使用 OPENSSL_RAW_DATA | OPENSSL_ZERO_PADDING，
// 因此 phpgo 需要支持零填充模式（不做额外填充）。
func opensslAESECB(a []Value, data, key []byte, encrypt bool) (Value, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return NewString(""), err
	}
	blockSize := block.BlockSize() // 16

	// 判断 options：OPENSSL_RAW_DATA=1, OPENSSL_ZERO_PADDING=3
	// OPENSSL_RAW_DATA | OPENSSL_ZERO_PADDING = 1 | 3 = 3
	// 当 options 包含 OPENSSL_ZERO_PADDING (3) 时，不做 PKCS7 填充
	zeroPad := false
	if len(a) >= 4 {
		opts := a[3].ToInt()
		// OPENSSL_RAW_DATA = 1 (bit 0)
		// OPENSSL_ZERO_PADDING = 2 (bit 1)
		// 只有当 bit 1 (OPENSSL_ZERO_PADDING) 被设置时才不做 PKCS7 填充
		if opts&2 != 0 {
			zeroPad = true
		}
	}

	if encrypt {
		if !zeroPad {
			// PKCS7 填充
			pad := blockSize - len(data)%blockSize
			if pad == blockSize {
				pad = blockSize // 仍需填充一个完整块
			}
			padded := make([]byte, len(data)+pad)
			copy(padded, data)
			for i := len(data); i < len(padded); i++ {
				padded[i] = byte(pad)
			}
			data = padded
		}
		// ECB 加密
		if len(data)%blockSize != 0 {
			return NewString(""), fmt.Errorf("openssl: AES-ECB 数据长度不是 %d 的倍数", blockSize)
		}
		encrypted := make([]byte, len(data))
		for i := 0; i < len(data); i += blockSize {
			block.Encrypt(encrypted[i:i+blockSize], data[i:i+blockSize])
		}
		return NewString(string(encrypted)), nil
	}

	// 解密
	if len(data)%blockSize != 0 {
		return NewString(""), fmt.Errorf("openssl: AES-ECB 解密数据长度不是 %d 的倍数", blockSize)
	}
	decrypted := make([]byte, len(data))
	for i := 0; i < len(data); i += blockSize {
		block.Decrypt(decrypted[i:i+blockSize], data[i:i+blockSize])
	}
	if !zeroPad {
		// 去除 PKCS7 填充
		if len(decrypted) > 0 {
			pad := int(decrypted[len(decrypted)-1])
			if pad > 0 && pad <= blockSize && pad <= len(decrypted) {
				decrypted = decrypted[:len(decrypted)-pad]
			}
		}
	}
	return NewString(string(decrypted)), nil
}

// 确保使用 cipher 包（用于潜在的未来 CBC 等模式）
var _ = cipher.NewCBCEncrypter
