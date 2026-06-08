package server

import (
	"crypto/tls"
	"fmt"
	"os"
	"strings"
	"sync"
)

var (
	cipherSuiteMap     map[string]uint16
	buildCipherMapOnce sync.Once

	// 缓存 TLS 证书，避免每次握手都从磁盘加载
	cachedCert   *tls.Certificate
	cachedCertMu sync.RWMutex
	certFile     string
	keyFile      string
	certModTime  int64
)

// ==================== TLS 配置解析 ====================
func parseProtocols(protoStr string) (minVersion, maxVersion uint16) {
	minVersion = tls.VersionTLS12
	maxVersion = tls.VersionTLS13
	if protoStr == "" {
		return
	}
	for _, p := range strings.Fields(protoStr) {
		switch strings.ToUpper(p) {
		case "TLSV1.2":
			if minVersion > tls.VersionTLS12 {
				minVersion = tls.VersionTLS12
			}
		case "TLSV1.3":
			if minVersion > tls.VersionTLS13 {
				minVersion = tls.VersionTLS13
			}
			maxVersion = tls.VersionTLS13
		}
	}
	return
}

func buildCipherMap() {
	cipherSuiteMap = make(map[string]uint16)

	// 仅获取安全套件
	for _, suite := range tls.CipherSuites() {
		cipherSuiteMap[suite.Name] = suite.ID
	}

	// 不再添加不安全套件
	// 提示：代码已移除 InsecureCipherSuites 防止不安全套件被应用。
	// 可以在 parseCipherSuites 的警告中说明不安全请求被忽略。

	// 确保 TLS 1.3 套件在旧版本 Go 中可用
	cipherSuiteMap["TLS_AES_128_GCM_SHA256"] = tls.TLS_AES_128_GCM_SHA256
	cipherSuiteMap["TLS_AES_256_GCM_SHA384"] = tls.TLS_AES_256_GCM_SHA384
	cipherSuiteMap["TLS_CHACHA20_POLY1305_SHA256"] = tls.TLS_CHACHA20_POLY1305_SHA256
	cipherSuiteMap["TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305"] = tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305
	cipherSuiteMap["TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305"] = tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305
}

func cipherNameToID(name string) uint16 {
	buildCipherMapOnce.Do(buildCipherMap)
	return cipherSuiteMap[name]
}

func parseCipherSuites(cipherStr string) []uint16 {
	if cipherStr == "" {
		return nil // 使用 Go 默认
	}
	var suites []uint16
	for _, c := range strings.Split(cipherStr, ":") {
		c = strings.TrimSpace(c)
		if cs := cipherNameToID(c); cs != 0 {
			suites = append(suites, cs)
		} else {
			fmt.Printf("警告: 未知的 Cipher Suite: %s\n", c)
		}
	}
	return suites
}

func parseCurvePreferences(curveStr string) []tls.CurveID {
	if curveStr == "" {
		return nil // Go 1.24 默认启用 X25519MLKEM768
	}
	var curves []tls.CurveID
	items := strings.FieldsFunc(curveStr, func(r rune) bool {
		return r == ':' || r == ';' || r == ','
	})
	for _, c := range items {
		c = strings.ToUpper(strings.TrimSpace(c))
		switch c {
		case "X25519":
			curves = append(curves, tls.X25519)
		case "P-256", "P256", "SECP256R1":
			curves = append(curves, tls.CurveP256)
		case "P-384", "P384", "SECP384R1":
			curves = append(curves, tls.CurveP384)
		case "P-521", "P521", "SECP521R1":
			curves = append(curves, tls.CurveP521)
		case "X25519MLKEM768":
			curves = append(curves, tls.X25519MLKEM768)
		default:
			fmt.Printf("警告: 未知或不支持的曲线: %s\n", c)
		}
	}
	if len(curves) == 0 {
		return nil // 默认
	}
	return curves
}

// ==================== 启动 HTTP 服务器 ====================

// 动态证书加载（带缓存，避免每次握手都从磁盘读取）
func makeTLSConfig(cFile, kFile string, minVersion, maxVersion uint16, cipherSuites []uint16, curves []tls.CurveID) *tls.Config {
	certFile = cFile
	keyFile = kFile
	return &tls.Config{
		MinVersion:       minVersion,
		MaxVersion:       maxVersion,
		CipherSuites:     cipherSuites,
		CurvePreferences: curves,
		NextProtos:       []string{"h3", "h2", "http/1.1"},
		GetCertificate: func(chi *tls.ClientHelloInfo) (*tls.Certificate, error) {
			return getCachedCert()
		},
	}
}

// getCachedCert 获取缓存的证书，文件修改时自动重新加载
func getCachedCert() (*tls.Certificate, error) {
	// 先尝试读锁快速路径
	cachedCertMu.RLock()
	if cachedCert != nil {
		// 检查文件是否修改
		info, err := os.Stat(certFile)
		if err == nil && info.ModTime().Unix() == certModTime {
			cert := cachedCert
			cachedCertMu.RUnlock()
			return cert, nil
		}
		cachedCertMu.RUnlock()
	}

	// 需要重新加载，获取写锁
	cachedCertMu.Lock()
	defer cachedCertMu.Unlock()

	// 双重检查：可能其他 goroutine 已经加载了
	if cachedCert != nil {
		info, err := os.Stat(certFile)
		if err == nil && info.ModTime().Unix() == certModTime {
			return cachedCert, nil
		}
	}

	// 加载证书
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("加载证书失败: %w", err)
	}

	// 更新缓存
	if info, err := os.Stat(certFile); err == nil {
		certModTime = info.ModTime().Unix()
	}
	cachedCert = &cert
	return &cert, nil
}
