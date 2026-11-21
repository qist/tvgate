// sign-release 为升级包生成 Ed25519 签名。
//
// 签名对象是 zip 文件的 SHA-256 摘要，输出 base64（可写入 <包>.sig 并随 Release 发布）。
// 客户端 updater 内置公钥（构建参数 -X ...updatePublicKeyHex=<公钥>）后，
// 会强制校验 <包>.sig；未内置公钥的构建只校验结构与发布页 SHA-256。
//
// 用法：
//
//	TVGATE_SIGNING_KEY=<32 字节种子的 hex> go run ./scripts/sign-release build/TVGate-linux-64.zip
//	TVGATE_SIGNING_KEY=... go run ./scripts/sign-release 包路径 输出.sig
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "用法: TVGATE_SIGNING_KEY=<hex> sign-release <包路径> [输出.sig]")
		os.Exit(2)
	}
	pkg := os.Args[1]
	out := pkg + ".sig"
	if len(os.Args) > 2 {
		out = os.Args[2]
	}

	seedHex := strings.TrimSpace(os.Getenv("TVGATE_SIGNING_KEY"))
	if seedHex == "" {
		fmt.Fprintln(os.Stderr, "未设置 TVGATE_SIGNING_KEY")
		os.Exit(2)
	}
	seed, err := hex.DecodeString(seedHex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "TVGATE_SIGNING_KEY 不是合法 hex: %v\n", err)
		os.Exit(1)
	}
	var priv ed25519.PrivateKey
	switch len(seed) {
	case ed25519.SeedSize:
		priv = ed25519.NewKeyFromSeed(seed)
	case ed25519.PrivateKeySize:
		priv = ed25519.PrivateKey(seed)
	default:
		fmt.Fprintf(os.Stderr, "密钥长度应为 %d（种子）或 %d（私钥）字节，实际 %d\n",
			ed25519.SeedSize, ed25519.PrivateKeySize, len(seed))
		os.Exit(1)
	}

	digest, err := fileSHA256(pkg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取升级包失败: %v\n", err)
		os.Exit(1)
	}
	sig := ed25519.Sign(priv, digest)
	if err := os.WriteFile(out, []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "写入签名失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("已签名: %s -> %s\n公钥(hex): %s\n", pkg, out, hex.EncodeToString(priv.Public().(ed25519.PublicKey)))
}

func fileSHA256(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}
