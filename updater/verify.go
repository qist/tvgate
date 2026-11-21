package updater

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/qist/tvgate/config"
)

const (
	// maxUpdatePackageSize 限制升级包大小，避免被错误页/无限流写满磁盘。
	maxUpdatePackageSize = 256 << 20
	// minUpdatePackageSize 小于该值肯定不是完整程序包（发布包约 14~24MB，
	// 这里只做下限兜底，错误页/空响应远小于该值）。
	minUpdatePackageSize = 1 << 20
	// minUpdateBinarySize 是包内可执行文件的最小体积，用于区分真正的程序与占位文件。
	minUpdateBinarySize = 8 << 20
	// maxDigestSize 校验文件很小，限制读取避免恶意响应。
	maxDigestSize = 4 << 10
)

// updatePublicKeyHex 是内置的 Ed25519 公钥（32 字节，十六进制）。
// 通过构建参数注入：
//
//	-ldflags "-X github.com/qist/tvgate/updater.updatePublicKeyHex=<hex>"
//
// 为空表示本次构建未启用签名校验：此时仍校验 zip 结构、包内程序与发布页
// 的 SHA-256（.dgst），可以挡住 DNS 劫持/加速镜像返回错误页或错误文件。
// 配置公钥后，升级包必须附带 `.zip.sig`（对 zip 的 SHA-256 签名），
// 否则一律拒绝，避免攻击者通过镜像替换文件或剥离签名。
var updatePublicKeyHex string

var zipMagic = []byte{'P', 'K', 0x03, 0x04}

// ErrUpdatePackageInvalid 表示下载内容不是合法的升级包（被劫持/篡改/损坏）。
// 调用方应把错误原样展示给用户，并保留当前版本不动。
type ErrUpdatePackageInvalid struct{ Reason string }

func (e *ErrUpdatePackageInvalid) Error() string { return e.Reason }

func invalidPackage(format string, args ...interface{}) error {
	return &ErrUpdatePackageInvalid{Reason: "升级包校验失败（可能被 DNS 劫持或加速镜像篡改）: " + fmt.Sprintf(format, args...)}
}

// verifyUpdatePackage 校验下载文件确实是发布包，并包含目标程序。
// 这一步在解压之前执行：错误页（HTML/JSON）、被截断的文件、空文件都会在这里拦下，
// 不会再走到 unzip 才报 “zip: not a valid zip file”。
func verifyUpdatePackage(path, expectedBinary string) error {
	info, err := os.Stat(path)
	if err != nil {
		return invalidPackage("无法读取下载文件: %v", err)
	}
	if info.Size() < minUpdatePackageSize {
		return invalidPackage("文件只有 %d 字节，不是完整的升级包", info.Size())
	}
	if info.Size() > maxUpdatePackageSize {
		return invalidPackage("文件过大（%d 字节），拒绝解压", info.Size())
	}

	f, err := os.Open(path)
	if err != nil {
		return invalidPackage("无法打开下载文件: %v", err)
	}
	head := make([]byte, len(zipMagic))
	_, rerr := io.ReadFull(f, head)
	f.Close()
	if rerr != nil || string(head) != string(zipMagic) {
		return invalidPackage("文件头不是 ZIP 格式，请检查 DNS/代理是否把下载地址劫持到了错误页面")
	}

	r, err := zip.OpenReader(path)
	if err != nil {
		return invalidPackage("ZIP 结构损坏: %v", err)
	}
	defer r.Close()

	want := strings.ToLower(filepath.Base(expectedBinary))
	found := false
	for _, zf := range r.File {
		base := strings.ToLower(filepath.Base(zf.Name))
		if base != want && base != want+".exe" {
			continue
		}
		if zf.UncompressedSize64 < minUpdateBinarySize {
			return invalidPackage("包内程序 %s 体积异常（%d 字节）", zf.Name, zf.UncompressedSize64)
		}
		// 完整读一遍程序文件，顺带校验 CRC：被截断/损坏的包会在这里失败。
		rc, err := zf.Open()
		if err != nil {
			return invalidPackage("无法读取包内程序: %v", err)
		}
		n, err := io.Copy(io.Discard, io.LimitReader(rc, maxUpdatePackageSize))
		rc.Close()
		if err != nil {
			return invalidPackage("包内程序校验失败: %v", err)
		}
		if n < minUpdateBinarySize {
			return invalidPackage("包内程序数据不完整（%d 字节）", n)
		}
		found = true
		break
	}
	if !found {
		return invalidPackage("包内缺少目标程序 %s", expectedBinary)
	}
	return nil
}

// sha256File 返回文件 SHA-256 的十六进制小写字符串。
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// parseSHA256Digest 解析发布页的 .dgst 文件。
// 兼容 "SHA2-256= <hex>"、"SHA256: <hex>" 以及只包含一行 hex 的格式。
func parseSHA256Digest(data []byte) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		value := line
		if i := strings.Index(line, "="); i >= 0 {
			key := strings.ToUpper(strings.TrimSpace(line[:i]))
			if key != "SHA2-256" && key != "SHA256" && key != "SHA-256" {
				continue
			}
			value = strings.TrimSpace(line[i+1:])
		} else if i := strings.Index(line, ":"); i >= 0 {
			key := strings.ToUpper(strings.TrimSpace(line[:i]))
			if key != "SHA2-256" && key != "SHA256" && key != "SHA-256" {
				continue
			}
			value = strings.TrimSpace(line[i+1:])
		} else if len(line) != 64 {
			// 没有字段名也不是单行 hex，跳过
			continue
		}
		if _, err := hex.DecodeString(value); err != nil || len(value) != 64 {
			continue
		}
		return strings.ToLower(value), nil
	}
	return "", fmt.Errorf("未找到 SHA-256 校验值")
}

// releaseAssetURL 优先使用官方 HTTPS 地址，其次才是配置的加速镜像：
// 校验文件从官方地址获取，才能发现镜像/中间人替换了升级包。
func releaseAssetURL(cfg config.GithubConfig, version, name string) []string {
	official := fmt.Sprintf("https://github.com/qist/tvgate/releases/download/%s/%s", version, name)
	urls := []string{official}
	if cfg.Enabled {
		if cfg.URL != "" {
			urls = append(urls, BuildURL(cfg.URL, official))
		}
		for _, b := range cfg.BackupURLs {
			if b != "" {
				urls = append(urls, BuildURL(b, official))
			}
		}
	}
	return urls
}

func fetchAsset(cfg config.GithubConfig, version, name string, limit int64) ([]byte, error) {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	client := &http.Client{Timeout: timeout}

	var lastErr error
	for _, u := range releaseAssetURL(cfg, version, name) {
		req, err := http.NewRequest("GET", u, nil)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("User-Agent", "TVGate-Updater")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("%s 返回 %d", u, resp.StatusCode)
			continue
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if int64(len(data)) > limit {
			lastErr = fmt.Errorf("%s 响应过大", u)
			continue
		}
		return data, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("没有可用的下载地址")
	}
	return nil, lastErr
}

// verifyReleaseDigest 用发布页的 .dgst 校验升级包 SHA-256。
// 返回 (是否取到了校验文件, 错误)。取不到时调用方可以继续（已有结构与程序校验），
// 但校验值不匹配一律拒绝。
func verifyReleaseDigest(cfg config.GithubConfig, version, zipName, zipPath string) (bool, error) {
	data, err := fetchAsset(cfg, version, zipName+".dgst", maxDigestSize)
	if err != nil {
		return false, nil
	}
	want, err := parseSHA256Digest(data)
	if err != nil {
		return false, nil
	}
	if err := verifyDigestValue(zipPath, want); err != nil {
		return true, err
	}
	return true, nil
}

// verifyDigestValue 比较文件 SHA-256 与给定十六进制值。
func verifyDigestValue(path, want string) error {
	got, err := sha256File(path)
	if err != nil {
		return invalidPackage("计算文件哈希失败: %v", err)
	}
	if !strings.EqualFold(got, want) {
		return invalidPackage("SHA-256 不匹配（期望 %s，实际 %s）", want, got)
	}
	return nil
}

// verifyReleaseSignature 在构建注入了公钥时校验 Ed25519 签名。
// 签名文件内容为 base64，签名对象是 zip 的 SHA-256 摘要。
func verifyReleaseSignature(cfg config.GithubConfig, version, zipName, zipPath string) error {
	if updatePublicKeyHex == "" {
		return nil
	}
	key, err := hex.DecodeString(strings.TrimSpace(updatePublicKeyHex))
	if err != nil || len(key) != ed25519.PublicKeySize {
		return invalidPackage("内置公钥无效，拒绝升级")
	}

	data, err := fetchAsset(cfg, version, zipName+".sig", maxDigestSize)
	if err != nil {
		return invalidPackage("缺少签名文件 %s.sig: %v", zipName, err)
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		return invalidPackage("签名格式错误")
	}
	return verifySignatureValue(ed25519.PublicKey(key), zipPath, sig)
}

// verifySignatureValue 用公钥校验 zip 的 Ed25519 签名（签名对象为文件 SHA-256 摘要）。
func verifySignatureValue(key ed25519.PublicKey, zipPath string, sig []byte) error {
	if len(sig) != ed25519.SignatureSize {
		return invalidPackage("签名格式错误")
	}
	sum, err := sha256File(zipPath)
	if err != nil {
		return invalidPackage("计算文件哈希失败: %v", err)
	}
	digest, err := hex.DecodeString(sum)
	if err != nil {
		return invalidPackage("计算文件哈希失败: %v", err)
	}
	if !ed25519.Verify(key, digest, sig) {
		return invalidPackage("签名验证失败，升级包不是官方发布内容")
	}
	return nil
}
