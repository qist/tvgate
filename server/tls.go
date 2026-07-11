package server

import (
	"crypto/tls"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/qist/tvgate/logger"
)

var (
	cipherSuiteMap     map[string]uint16
	buildCipherMapOnce sync.Once
)

// certEntry 缓存单个证书及其文件修改时间
type certEntry struct {
	cert     *tls.Certificate
	certMod  time.Time // certFile 的 mtime
	keyMod   time.Time // keyFile 的 mtime
	lastStat time.Time // 上次 stat 检查时间
}

var (
	certCacheMu  sync.RWMutex
	certCache    = make(map[string]*certEntry)
	certStatTTL  = 10 * time.Second // stat 检查间隔，stat 本身非常轻量
)

// getCachedCertificate 获取缓存的 TLS 证书，通过 mtime 检测文件变更
// 策略：
//   - 缓存命中时，每隔 statTTL 做一次 os.Stat 检查 mtime
//   - mtime 未变 → 直接返回缓存（零磁盘读取）
//   - mtime 变了 → 重新 LoadX509KeyPair 并更新缓存
//   - stat 失败 → 返回旧缓存（容错）
func getCachedCertificate(certFile, keyFile string) (*tls.Certificate, error) {
	cacheKey := certFile + "|" + keyFile
	now := time.Now()

	// 快速路径：读锁检查是否需要 stat
	certCacheMu.RLock()
	entry, ok := certCache[cacheKey]
	certCacheMu.RUnlock()

	if ok && now.Sub(entry.lastStat) < certStatTTL {
		// 还没到 stat 检查时间，直接返回缓存
		return entry.cert, nil
	}

	// 需要检查文件是否变更
	certStat, certErr := os.Stat(certFile)
	keyStat, keyErr := os.Stat(keyFile)

	if certErr != nil || keyErr != nil {
		// stat 失败，如果有旧缓存就用旧的
		if ok {
			return entry.cert, nil
		}
		return nil, fmt.Errorf("证书文件不存在: cert=%v key=%v", certErr, keyErr)
	}

	certMod := certStat.ModTime()
	keyMod := keyStat.ModTime()

	// 如果缓存存在且 mtime 未变，只更新 lastStat
	if ok && certMod.Equal(entry.certMod) && keyMod.Equal(entry.keyMod) {
		certCacheMu.Lock()
		entry.lastStat = now
		certCacheMu.Unlock()
		return entry.cert, nil
	}

	// 文件变更或首次加载，重新读取证书
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		// 加载失败，如果有旧缓存就用旧的（容错）
		if ok {
			logger.LogPrintf("⚠️ 证书加载失败，使用旧缓存: %v", err)
			return entry.cert, nil
		}
		return nil, fmt.Errorf("加载证书失败: %w", err)
	}

	newEntry := &certEntry{
		cert:     &cert,
		certMod:  certMod,
		keyMod:   keyMod,
		lastStat: now,
	}

	certCacheMu.Lock()
	certCache[cacheKey] = newEntry
	certCacheMu.Unlock()

	if ok {
		logger.LogPrintf("🔄 证书已更新: %s", certFile)
	}

	return &cert, nil
}

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

// 动态证书加载
func makeTLSConfig(certFile, keyFile string, minVersion, maxVersion uint16, cipherSuites []uint16, curves []tls.CurveID) *tls.Config {
	return &tls.Config{
		MinVersion:       minVersion,
		MaxVersion:       maxVersion,
		CipherSuites:     cipherSuites,
		CurvePreferences: curves,
		NextProtos:       []string{"h3", "h2", "http/1.1"},
		GetCertificate: func(chi *tls.ClientHelloInfo) (*tls.Certificate, error) {
			return getCachedCertificate(certFile, keyFile)
		},
	}
}
