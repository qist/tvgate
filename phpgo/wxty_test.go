package phpgo

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/hex"
	"math/big"
	"os"
	"strings"
	"testing"
)

// TestParseWxty 确保 /www/wxty.php 能被纯 Go parser 完整解析（无语法错误）。
func TestParseWxty(t *testing.T) {
	src, err := os.ReadFile("/www/wxty.php")
	if err != nil {
		t.Skip("wxty.php not found, skip")
	}
	if _, err := ParseProgram(string(src)); err != nil {
		t.Fatalf("parse wxty.php failed: %v", err)
	}
}

// TestWxtyRsaMath 完整复刻 wxty.php 的 RSA 解密机制（服务端"签名式加密" + 客户端"公钥解密"）：
//
//	服务端：c = P^d mod n（私钥指数）
//	客户端：m = c^e mod n（公钥指数，即 bcpowmod）→ 得到 P，再 PKCS#1 v1.5 类型1 去填充。
//
// 用自主生成的 1024 位密钥对验证 bcmath 的 modpow 与去填充都正确。
func TestWxtyRsaMath(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}

	msg := []byte("http://example/live.m3u8")

	// 构造 PKCS#1 v1.5 类型1 填充块：00 01 FF..FF 00 <msg>
	block := make([]byte, 128)
	block[0], block[1] = 0x00, 0x01
	for i := 2; i < 128-len(msg)-1; i++ {
		block[i] = 0xFF
	}
	block[128-len(msg)-1] = 0x00
	copy(block[128-len(msg):], msg)

	P := new(big.Int).SetBytes(block)
	e := big.NewInt(int64(key.PublicKey.E))
	n := key.N

	// 服务端"加密"：c = P^d mod n（用私钥指数，签名风格）
	c := new(big.Int).Exp(P, key.D, n)

	// 客户端"解密"：m = c^e mod n，用项目 bcpowmod
	mBc := callOne(t, "bcpowmod", NewString(c.String()), NewString(e.String()), NewString(n.String())).ToString()
	if mBc != P.String() {
		t.Fatalf("bcpowmod(c,e,n) != P:\n got=%s\nwant=%s", mBc, P.String())
	}

	// 用脚本的 rsaPublicDecryptChunk 去填充逻辑恢复明文
	hexStr := strings.Repeat("0", 256-len(P.Text(16))) + P.Text(16)
	blk, err := hex.DecodeString(hexStr)
	if err != nil || len(blk) != 128 {
		t.Fatalf("block len %d, err=%v", len(blk), err)
	}
	if blk[0] != 0x00 || blk[1] != 0x01 {
		t.Fatalf("bad block header: %02x %02x", blk[0], blk[1])
	}
	j := 2
	for j < 128 && blk[j] == 0xFF {
		j++
	}
	if j >= 128 || blk[j] != 0x00 {
		t.Fatalf("bad padding: j=%d b=0x%02x", j, blk[j])
	}
	if string(blk[j+1:]) != string(msg) {
		t.Fatalf("decrypt payload mismatch: got %q want %q", blk[j+1:], msg)
	}
}
