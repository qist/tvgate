package phpgo

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

// aesCBC PKCS7 加/解密（供 stub 使用，模拟服务端）
func aesCBC(data, key, iv []byte, encrypt bool) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if encrypt {
		pad := 16 - len(data)%16
		pt := make([]byte, pad)
		for i := range pt {
			pt[i] = byte(pad)
		}
		data = append(data, pt...)
		out := make([]byte, len(data))
		cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, data)
		return out, nil
	}
	out := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, data)
	if len(out) > 0 {
		pad := int(out[len(out)-1])
		if pad > 0 && pad <= 16 && pad <= len(out) {
			out = out[:len(out)-pad]
		}
	}
	return out, nil
}

// stubProxy 模拟 4gtv API，验证 PHP 完整逻辑链路（openssl + curl + json）。
func stubProxy(method, url string, opts *CurlOptions) (*ProxyResult, error) {
	key := []byte("ilyB29ZdruuQjC45JhBBR7o2Z8WJ26Vg")
	iv := []byte("JUMxvVMmszqUTeKn")
	if strings.Contains(url, "/Channel/GetChannel/") {
		return &ProxyResult{Body: `{"Data":{"fnID":31,"fs4GTV_ID":"litv-ftv13"}}`}, nil
	}
	if strings.Contains(url, "GetChannelUrl3") {
		inner := `{"flstURLs":["https://cdn.example.com/live/stream.m3u8"]}`
		enc, _ := aesCBC([]byte(inner), key, iv, true)
		b64 := base64.StdEncoding.EncodeToString(enc)
		return &ProxyResult{Body: `{"Data":"` + b64 + `"}`}, nil
	}
	if strings.Contains(url, "GetURL.ashx") {
		// 模拟服务端：VideoURL = hexiv(16) + base64(aes_cbc(plaintext))
		hexkey := []byte("VxzAfiseH0AbLShkQOPwdsssw5KyLeuv")
		hexiv := []byte("1234567890123456")
		plaintext := "https://cdn.example.com/live/vpn.m3u8"
		enc, _ := aesCBC([]byte(plaintext), hexkey, hexiv, true)
		b64 := base64.StdEncoding.EncodeToString(enc)
		vUrl := string(hexiv) + b64
		return &ProxyResult{Body: `{"VideoURL":"` + vUrl + `"}`}, nil
	}
	return &ProxyResult{Body: ``}, nil
}

func Test4gtvPoC(t *testing.T) {
	src, err := os.ReadFile("/www/4gtv.php")
	if err != nil {
		t.Skip("4gtv.php 不在 /www，跳过: " + err.Error())
	}
	env, err := Execute(string(src), stubProxy, func(e *Env) {
		e.SetGet("channel", "4gtv-4gtv001") // 命中首个 if 分支（关联数组 key）
	})
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if !env.ExitCalled() {
		t.Fatalf("期望触发 exit()，实际未触发")
	}
	// 验证捕获的 header 包含 Location: https://cdn.example.com/live/vpn.m3u8
	// （命中 else if 分支：curl_get(GetURL.ashx) → findString → openssl_decrypt 链路）
	found := false
	for _, h := range env.Headers() {
		if strings.HasPrefix(h, "Location: https://cdn.example.com/live/vpn.m3u8") {
			found = true
		}
	}
	if !found {
		t.Fatalf("未捕获到预期的 Location header，实际 headers=%v echo=%s", env.Headers(), env.EchoOutput())
	}
	t.Logf("✅ 4gtv.php PoC 通过：openssl AES-CBC + curl(SOCKS5代理→Go) + findString + json 全链路正确，Location=%s",
		env.Headers()[0])
}
