package phpgo

import (
	"crypto/cipher"
	"crypto/des"
	"fmt"
)

// opensslTripleDESECB 实现 3DES ECB 模式加解密
// PHP 的 des-ede3 使用 24 字节密钥，ECB 模式，PKCS7 填充
func opensslTripleDESECB(a []Value, data, key []byte, encrypt bool) (Value, error) {
	if len(key) != 24 {
		return NewString(""), fmt.Errorf("openssl: des-ede3 密钥长度必须为 24 字节，实际 %d", len(key))
	}
	block, err := des.NewTripleDESCipher(key)
	if err != nil {
		return NewString(""), err
	}
	blockSize := block.BlockSize() // 8

	if encrypt {
		// PKCS7 填充
		pad := blockSize - len(data)%blockSize
		padded := make([]byte, len(data)+pad)
		copy(padded, data)
		for i := len(data); i < len(padded); i++ {
			padded[i] = byte(pad)
		}
		// ECB 加密
		encrypted := make([]byte, len(padded))
		for i := 0; i < len(padded); i += blockSize {
			block.Encrypt(encrypted[i:i+blockSize], padded[i:i+blockSize])
		}
		return NewString(string(encrypted)), nil
	}

	// 解密
	if len(data)%blockSize != 0 {
		return NewString(""), fmt.Errorf("openssl: des-ede3 解密数据长度不是 %d 的倍数", blockSize)
	}
	decrypted := make([]byte, len(data))
	for i := 0; i < len(data); i += blockSize {
		block.Decrypt(decrypted[i:i+blockSize], data[i:i+blockSize])
	}
	// 去除 PKCS7 填充
	if len(decrypted) > 0 {
		pad := int(decrypted[len(decrypted)-1])
		if pad > 0 && pad <= blockSize {
			decrypted = decrypted[:len(decrypted)-pad]
		}
	}
	return NewString(string(decrypted)), nil
}

// opensslTripleDESCBC 实现 3DES CBC 模式加解密
func opensslTripleDESCBC(a []Value, data, key, iv []byte, encrypt bool) (Value, error) {
	if len(key) != 24 {
		return NewString(""), fmt.Errorf("openssl: des-ede3-cbc 密钥长度必须为 24 字节，实际 %d", len(key))
	}
	block, err := des.NewTripleDESCipher(key)
	if err != nil {
		return NewString(""), err
	}
	blockSize := block.BlockSize() // 8

	if len(iv) == 0 {
		iv = make([]byte, blockSize)
	} else if len(iv) < blockSize {
		padded := make([]byte, blockSize)
		copy(padded, iv)
		iv = padded
	} else if len(iv) > blockSize {
		iv = iv[:blockSize]
	}

	if encrypt {
		// PKCS7 填充
		pad := blockSize - len(data)%blockSize
		padded := make([]byte, len(data)+pad)
		copy(padded, data)
		for i := len(data); i < len(padded); i++ {
			padded[i] = byte(pad)
		}
		encrypted := make([]byte, len(padded))
		mode := cipher.NewCBCEncrypter(block, iv)
		mode.CryptBlocks(encrypted, padded)
		return NewString(string(encrypted)), nil
	}

	// 解密
	if len(data)%blockSize != 0 {
		return NewString(""), fmt.Errorf("openssl: des-ede3-cbc 解密数据长度不是 %d 的倍数", blockSize)
	}
	decrypted := make([]byte, len(data))
	mode := cipher.NewCBCDecrypter(block, iv)
	mode.CryptBlocks(decrypted, data)
	// 去除 PKCS7 填充
	if len(decrypted) > 0 {
		pad := int(decrypted[len(decrypted)-1])
		if pad > 0 && pad <= blockSize {
			decrypted = decrypted[:len(decrypted)-pad]
		}
	}
	return NewString(string(decrypted)), nil
}
