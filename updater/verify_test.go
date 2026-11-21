package updater

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTestZip 生成一个包含指定程序名的 zip。
// 内容用随机字节，避免高度可压缩导致压缩后体积过小、触发包体积下限。
func writeTestZip(t *testing.T, path, name string, size int64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1<<20)
	written := int64(0)
	for written < size {
		chunk := int64(len(buf))
		if remain := size - written; remain < chunk {
			chunk = remain
		}
		if _, err := rand.Read(buf[:chunk]); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(buf[:chunk]); err != nil {
			t.Fatal(err)
		}
		written += chunk
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// 回归：DNS 劫持/加速镜像返回的 HTML 错误页必须在解压前被识别，
// 而不是等到 zip.OpenReader 报 “zip: not a valid zip file”。
func TestVerifyUpdatePackageRejectsHijackedHTML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "TVGate-linux-64.zip")
	if err := os.WriteFile(path, []byte("<!DOCTYPE html><html><body>blocked</body></html>"), 0644); err != nil {
		t.Fatal(err)
	}
	// 补足到最小体积，确保走的是文件头判断而不是体积判断
	if err := os.Truncate(path, minUpdatePackageSize+1); err != nil {
		t.Fatal(err)
	}

	err := verifyUpdatePackage(path, "TVGate-linux-64")
	if err == nil {
		t.Fatal("HTML 错误页应被拒绝")
	}
	if _, ok := err.(*ErrUpdatePackageInvalid); !ok {
		t.Fatalf("错误类型应为 ErrUpdatePackageInvalid，实际 %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "ZIP") && !strings.Contains(err.Error(), "DNS") {
		t.Fatalf("错误提示应说明不是 ZIP/可能被劫持: %v", err)
	}
}

func TestVerifyUpdatePackageAcceptsValidZip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "TVGate-linux-64.zip")
	writeTestZip(t, path, "TVGate-linux-64", minUpdateBinarySize+1024)
	if err := verifyUpdatePackage(path, "TVGate-linux-64"); err != nil {
		t.Fatalf("合法升级包被拒绝: %v", err)
	}
}

func TestVerifyUpdatePackageRejectsMissingBinary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "TVGate-linux-64.zip")
	writeTestZip(t, path, "README.md", 1024)
	if err := verifyUpdatePackage(path, "TVGate-linux-64"); err == nil {
		t.Fatal("缺少目标程序的包应被拒绝")
	}
}

func TestParseSHA256Digest(t *testing.T) {
	sum := strings.Repeat("a1", 32)
	cases := []struct {
		name string
		data string
		want string
	}{
		{"dgst 格式", "MD5= deadbeef\nSHA1= cafe\nSHA2-256= " + sum + "\nSHA2-512= ff\n", sum},
		{"冒号格式", "sha256: " + strings.ToUpper(sum) + "\n", sum},
		{"单行 hex", sum + "\n", sum},
	}
	for _, tc := range cases {
		got, err := parseSHA256Digest([]byte(tc.data))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("%s: got %s want %s", tc.name, got, tc.want)
		}
	}
	if _, err := parseSHA256Digest([]byte("MD5= deadbeef\n")); err == nil {
		t.Fatal("没有 SHA-256 时应返回错误")
	}
}

func TestVerifyDigestValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pkg.zip")
	if err := os.WriteFile(path, []byte("payload"), 0644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("payload"))
	ok := hex.EncodeToString(sum[:])

	if err := verifyDigestValue(path, ok); err != nil {
		t.Fatalf("正确哈希被拒绝: %v", err)
	}
	if err := verifyDigestValue(path, strings.Repeat("0", 64)); err == nil {
		t.Fatal("哈希不匹配应被拒绝")
	}
}

func TestVerifySignatureValue(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "pkg.zip")
	if err := os.WriteFile(path, []byte("official payload"), 0644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("official payload"))
	sig := ed25519.Sign(priv, sum[:])

	if err := verifySignatureValue(pub, path, sig); err != nil {
		t.Fatalf("合法签名被拒绝: %v", err)
	}

	if err := os.WriteFile(path, []byte("tampered"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifySignatureValue(pub, path, sig); err == nil {
		t.Fatal("文件被篡改后签名应校验失败")
	}

	otherPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("official payload"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifySignatureValue(otherPub, path, sig); err == nil {
		t.Fatal("其他公钥不应通过校验")
	}
}
