package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/monitor"
)

// 全局Token管理器
// 全局Token管理器
var GlobalTokenManager *TokenManager
var globalMu sync.RWMutex

// GetGlobalTokenManager 安全获取全局Token管理器
func GetGlobalTokenManager() *TokenManager {
	globalMu.RLock()
	defer globalMu.RUnlock()
	// logger.LogPrintf("获取全局Token管理器，是否存在: %v", GlobalTokenManager != nil)
	if GlobalTokenManager != nil {
		// logger.LogPrintf("全局Token管理器静态token数量: %d", len(GlobalTokenManager.StaticTokens))
	}
	return GlobalTokenManager
}

// NewGlobalTokenManagerFromConfig 从全局配置创建Token管理器
func NewGlobalTokenManagerFromConfig(globalAuth *config.AuthConfig) *TokenManager {
	// 创建域映射配置
	domainConfig := &DomainMapConfig{
		Auth: *globalAuth,
	}

	// 使用域映射配置创建并返回Token管理器
	return NewTokenManagerFromConfig(domainConfig)
}

// ReloadGlobalTokenManager 根据最新配置重载全局Token管理器
func ReloadGlobalTokenManager(cfg *config.AuthConfig) {
	globalMu.Lock()
	defer globalMu.Unlock()

	// logger.LogPrintf("🔄 开始重载全局Token管理器")
	// logger.LogPrintf("全局认证是否启用: %v", cfg.TokensEnabled)
	// logger.LogPrintf("全局认证Token参数名: %s", cfg.TokenParamName)
	// logger.LogPrintf("全局静态Token启用状态: %v", cfg.StaticTokens.EnableStatic)
	// logger.LogPrintf("全局静态Token值: %s", cfg.StaticTokens.Token)
	// logger.LogPrintf("全局静态Token过期时间: %v", cfg.StaticTokens.ExpireHours)
	// logger.LogPrintf("全局动态Token启用状态: %v", cfg.DynamicTokens.EnableDynamic)
	// logger.LogPrintf("全局动态Token密钥: %s", cfg.DynamicTokens.Secret)
	// logger.LogPrintf("全局动态Token盐值: %s", cfg.DynamicTokens.Salt)
	// logger.LogPrintf("全局动态Token TTL: %v", cfg.DynamicTokens.DynamicTTL)

	if !cfg.TokensEnabled {
		// logger.LogPrintf("全局认证未启用，清空全局token管理器")
		GlobalTokenManager = nil
		return
	}

	// 创建新的TokenManager
	newTM := NewGlobalTokenManagerFromConfig(cfg)

	// logger.LogPrintf("新Token管理器创建完成，静态tokens数量: %d", len(newTM.StaticTokens))
	// for token := range newTM.StaticTokens {
	// logger.LogPrintf("新Token管理器中的静态token: %s", token)
	// }

	// 复用旧静态 token 状态
	if GlobalTokenManager != nil && GlobalTokenManager.StaticTokens != nil {
		// logger.LogPrintf("处理旧Token管理器状态，旧静态tokens数量: %d", len(GlobalTokenManager.StaticTokens))
		for token, oldSess := range GlobalTokenManager.StaticTokens {
			// logger.LogPrintf("处理旧静态token: %s", token)
			if sess, exists := newTM.StaticTokens[token]; exists {
				// 保留旧 session 的 FirstAccessAt、OriginalURL
				sess.FirstAccessAt = oldSess.FirstAccessAt
				sess.OriginalURL = oldSess.OriginalURL
				sess.LastActiveAt = oldSess.LastActiveAt
				// logger.LogPrintf("保留旧静态token状态: %s", token)
			} else {
				// 添加旧 token 到新 manager
				newTM.StaticTokens[token] = oldSess
				// logger.LogPrintf("添加旧静态token到新管理器: %s", token)
			}
		}
	} else {
		// logger.LogPrintf("没有旧的Token管理器或旧管理器中没有静态token")
	}

	// 替换全局管理器
	GlobalTokenManager = newTM
	// logger.LogPrintf("✅ 全局TokenManager已热更新完成，最终静态tokens数量: %d", len(GlobalTokenManager.StaticTokens))
	// for token := range GlobalTokenManager.StaticTokens {
	// logger.LogPrintf("最终全局Token管理器中的静态token: %s", token)
	// }
}

// CleanupGlobalTokenManager 清理过期 token，会定期调用
func CleanupGlobalTokenManager() {
	tm := GetGlobalTokenManager()
	if tm != nil {
		tm.CleanupExpiredSessions()
	}
}

// 用于存储静态token的全局状态，避免配置重载后丢失token状态
var staticTokenStates = make(map[string]*SessionInfo)
var staticTokenStatesMutex sync.RWMutex

// ---------------------------
// TokenManager 管理 token 与在线状态
// ---------------------------
type TokenManager struct {
	Enabled        bool
	TokenParamName string

	mu sync.RWMutex

	StaticTokens  map[string]*SessionInfo
	DynamicTokens map[string]*SessionInfo
	DynamicConfig *DynamicTokenConfig

	// 记录token类型，避免在错误的映射中查找
	tokenTypes map[string]string // "static" or "dynamic"
}

// SessionInfo 会话信息
type SessionInfo struct {
	Token          string
	ConnID         string
	FirstAccessAt  time.Time
	LastActiveAt   time.Time
	ExpireDuration time.Duration
	IP             string
	URL            string
	OriginalURL    string // 记录首次访问的URL
}

// DynamicTokenConfig 动态 token 配置
type DynamicTokenConfig struct {
	Secret string
	Salt   string
	TTL    time.Duration
}

// ---------------------------
// 初始化
// ---------------------------
func NewTokenManagerFromConfig(cfg *DomainMapConfig) *TokenManager {
	// logger.LogPrintf("创建新的Token管理器")
	// logger.LogPrintf("认证启用状态: %v", cfg.Auth.TokensEnabled)
	// logger.LogPrintf("Token参数名: %s", cfg.Auth.TokenParamName)
	// logger.LogPrintf("静态Token启用状态: %v", cfg.Auth.StaticTokens.EnableStatic)
	// logger.LogPrintf("静态Token值: %s", cfg.Auth.StaticTokens.Token)
	// logger.LogPrintf("静态Token过期时间: %v", cfg.Auth.StaticTokens.ExpireHours)
	// logger.LogPrintf("动态Token启用状态: %v", cfg.Auth.DynamicTokens.EnableDynamic)
	// logger.LogPrintf("动态Token密钥: %s", cfg.Auth.DynamicTokens.Secret)
	// logger.LogPrintf("动态Token盐值: %s", cfg.Auth.DynamicTokens.Salt)
	// logger.LogPrintf("动态Token TTL: %v", cfg.Auth.DynamicTokens.DynamicTTL)

	tm := &TokenManager{
		Enabled:        cfg.Auth.TokensEnabled,
		TokenParamName: cfg.Auth.TokenParamName,
		StaticTokens:   make(map[string]*SessionInfo),
		DynamicTokens:  make(map[string]*SessionInfo),
		tokenTypes:     make(map[string]string),
	}

	// 处理静态 token
	st := cfg.Auth.StaticTokens
	if st.EnableStatic && st.Token != "" {
		staticTokenStatesMutex.Lock()
		// 检查是否已经存在该token的状态
		if existingSession, exists := staticTokenStates[st.Token]; exists {
			// 复用已存在的会话信息
			tm.StaticTokens[st.Token] = existingSession
			// logger.LogPrintf("复用已存在的静态token会话信息: %s", st.Token)
		} else {
			// 创建新的会话信息
			newSession := &SessionInfo{
				Token:          st.Token,
				ExpireDuration: st.ExpireHours,
			}
			tm.StaticTokens[st.Token] = newSession
			staticTokenStates[st.Token] = newSession
			// logger.LogPrintf("创建新的静态token会话信息: %s", st.Token)
		}
		staticTokenStatesMutex.Unlock()

		tm.tokenTypes[st.Token] = "static"
		// logger.LogPrintf("设置token类型为静态: %s", st.Token)
	}

	// 处理动态 token 配置
	if cfg.Auth.DynamicTokens.EnableDynamic {
		tm.DynamicConfig = &DynamicTokenConfig{
			Secret: cfg.Auth.DynamicTokens.Secret,
			Salt:   cfg.Auth.DynamicTokens.Salt,
			TTL:    cfg.Auth.DynamicTokens.DynamicTTL,
		}
		// logger.LogPrintf("已配置动态token")
	}

	// logger.LogPrintf("Token管理器创建完成")
	return tm
}

// ---------------------------
// KeepAlive 更新会话信息
// ---------------------------
func (tm *TokenManager) KeepAlive(token, connID, ip, urlPath string) {
	if !tm.Enabled || token == "" || connID == "" {
		return
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()

	now := time.Now()

	updateSession := func(sess *SessionInfo) {
		if sess.FirstAccessAt.IsZero() { // 第一次访问初始化
			sess.FirstAccessAt = now
			sess.OriginalURL = urlPath
			sess.IP = ip
		}
		sess.ConnID = connID
		sess.IP = ip
		sess.URL = urlPath
		sess.LastActiveAt = now // 更新最后活跃时间
	}

	// 静态 token
	if tm.StaticTokens != nil {
		if sess, ok := tm.StaticTokens[token]; ok {
			updateSession(sess)
			return
		}
	}

	// 动态 token
	if tm.DynamicConfig != nil && tm.DynamicTokens != nil {
		if sess, ok := tm.DynamicTokens[token]; ok {
			updateSession(sess)
			return
		}
	}
}

// ---------------------------
// 验证 token（结合在线状态）
// ---------------------------
func (tm *TokenManager) ValidateToken(token, urlPath, connID string) bool {
	// logger.LogPrintf("开始验证token: %s, url: %s, connID: %s", token, urlPath, connID)
	// logger.LogPrintf("Token管理器启用状态: %v", tm.Enabled)
	// logger.LogPrintf("Token管理器地址: %p", tm)
	// logger.LogPrintf("全局Token管理器地址: %p", GetGlobalTokenManager())
	// logger.LogPrintf("是否为全局Token管理器: %v", tm == GetGlobalTokenManager())
	// logger.LogPrintf("Token管理器静态tokens数量: %d", len(tm.StaticTokens))

	// 打印所有可用的静态token用于调试
	// for t := range tm.StaticTokens {
		// logger.LogPrintf("可用静态token: %s", t)
	// }

	if !tm.Enabled {
		// logger.LogPrintf("Token管理器未启用，验证通过")
		return true
	}

	// 检查token参数是否为空
	if token == "" {
		// logger.LogPrintf("Token参数为空")
		return false
	}

	tm.mu.RLock()
	defer tm.mu.RUnlock()

	now := time.Now()
	// logger.LogPrintf("当前时间: %s", now.Format("2006-01-02 15:04:05"))

	// ---------------------------
	// session 是否过期检查
	// ---------------------------
	checkSession := func(sess *SessionInfo) bool {
		// 对于静态token，如果ExpireDuration为0，表示永不过期
		if _, isStatic := tm.StaticTokens[token]; isStatic && sess.ExpireDuration == 0 {
			// logger.LogPrintf("静态token永不过期")
			return true
		}

		// 对于静态token，如果没有设置首次访问时间，认为是有效的（因为静态token不需要会话跟踪）
		if _, isStatic := tm.StaticTokens[token]; isStatic && sess.FirstAccessAt.IsZero() {
			// logger.LogPrintf("静态token首次访问时间为空，但静态token不需要会话跟踪，验证通过")
			return true
		}

		// 对于动态token，必须有首次访问时间
		if sess.FirstAccessAt.IsZero() {
			// logger.LogPrintf("会话首次访问时间为空")
			return false
		}

		// 从monitor获取实际的最后活跃时间
		lastActiveAt := sess.LastActiveAt // 使用LastActiveAt而不是FirstAccessAt
		if lastActiveAt.IsZero() {
			lastActiveAt = sess.FirstAccessAt // 如果LastActiveAt为空则使用FirstAccessAt
		}

		// 从monitor获取实际的最后活跃时间
		if conn := monitor.ActiveClients.GetConnectionByID(sess.ConnID); conn != nil {
			lastActiveAt = conn.LastActive
		}

		// 判断过期逻辑：从最后活跃时间到现在超过了ExpireDuration
		if sess.ExpireDuration > 0 && now.Sub(lastActiveAt) > sess.ExpireDuration {
			// logger.LogPrintf("会话已过期: ExpireDuration=%v, LastActiveAt=%s, Now=%s",
				// sess.ExpireDuration, lastActiveAt.Format("2006-01-02 15:04:05"), now.Format("2006-01-02 15:04:05"))
			return false // 已过期
		}

		// logger.LogPrintf("会话未过期: ExpireDuration=%v, LastActiveAt=%s, Now=%s",
			// sess.ExpireDuration, lastActiveAt.Format("2006-01-02 15:04:05"), now.Format("2006-01-02 15:04:05"))
		return true // 未过期
	}

	// ---------------------------
	// 检查token类型（如果已记录）
	// ---------------------------
	tokenType, typeRecorded := tm.tokenTypes[token]
	// logger.LogPrintf("Token类型: %s, 是否已记录: %v", tokenType, typeRecorded)

	// ---------------------------
	// 如果记录为静态token或未记录类型，尝试验证静态 token
	// ---------------------------
	if !typeRecorded || tokenType == "static" {
		// logger.LogPrintf("尝试验证静态token")
		if tm.StaticTokens != nil {
			// logger.LogPrintf("静态tokens数量: %d", len(tm.StaticTokens))
			if sess, ok := tm.StaticTokens[token]; ok {
				// logger.LogPrintf("找到静态token: %s", token)
				// 记录token类型
				if !typeRecorded {
					tm.tokenTypes[token] = "static"
				}

				expired := !checkSession(sess)
				// 从monitor获取实际的最后活跃时间用于日志输出
				lastActiveAt := sess.LastActiveAt
				if lastActiveAt.IsZero() {
					lastActiveAt = sess.FirstAccessAt
				}
				if conn := monitor.ActiveClients.GetConnectionByID(sess.ConnID); conn != nil {
					lastActiveAt = conn.LastActive
				}

				logger.LogPrintf(
					"静态token验证结果: %s, url: %s, originalURL: %s, ip: %s, connID: %s, ExpireDuration: %s, FirstAccessAt: %s, LastActiveAt: %s, expired: %v",
					token,
					urlPath,
					sess.OriginalURL,
					sess.IP,
					connID,
					sess.ExpireDuration,
					sess.FirstAccessAt.Format("2006-01-02 15:04:05"),
					lastActiveAt.Format("2006-01-02 15:04:05"),
					expired,
				)
				return !expired
			} else {
				// logger.LogPrintf("静态token未找到: %s", token)
				// 打印所有可用的静态token用于调试
				// for t := range tm.StaticTokens {
					// logger.LogPrintf("可用静态token: %s", t)
				// }
			}
		}
	}

	// ---------------------------
	// 如果记录为动态token或未记录类型，尝试验证动态 token
	// ---------------------------
	if (!typeRecorded || tokenType == "dynamic") && tm.DynamicConfig != nil {
		// logger.LogPrintf("尝试验证动态token")
		// 记录token参数用于调试
		// logger.LogPrintf("尝试验证动态token: %s", token)

		// 修复URL解码过程中"+"被替换为空格的问题
		fixedToken := token
		if strings.Contains(token, " ") {
			fixedToken = strings.ReplaceAll(token, " ", "+")
			// logger.LogPrintf("修复URL解码问题: 原token包含空格，已替换为+")
		}

		// 尝试解密
		plain, err := aesDecryptBase64(fixedToken, tm.DynamicConfig.Secret)
		if err != nil {
			// logger.LogPrintf("动态token解密失败: %v, token长度: %d, token内容: %s", err, len(token), token)
			// 如果修复后的token仍然失败，记录详细信息
			if fixedToken != token {
				// logger.LogPrintf("修复后的token也失败，原始token: %s, 修复后token: %s", token, fixedToken)
			}
		} else {
			// 检查解密结果
			if plain != "" {
				parts := strings.SplitN(plain, "|", 3)
				if len(parts) == 3 {
					salt, _, tsStr := parts[0], parts[1], parts[2]
					// logger.LogPrintf("动态token解密成功: salt=%s, tsStr=%s", salt, tsStr)
					if salt == tm.DynamicConfig.Salt {
						// 不再检查路径是否匹配，始终使用token中存储的原始路径
						// 这样确保了第一次生成的路径一直有效，适用于301重定向、TS文件等场景

						tsUnix, err := strconv.ParseInt(tsStr, 10, 64)
						if err == nil {
							if tm.DynamicConfig.TTL <= 0 || time.Since(time.Unix(tsUnix, 0)) <= tm.DynamicConfig.TTL {
								// logger.LogPrintf("动态token未过期: TTL=%v, 创建时间=%s",
									// tm.DynamicConfig.TTL, time.Unix(tsUnix, 0).Format("2006-01-02 15:04:05"))
								// 动态token验证成功，存入动态 token 会话
								if !typeRecorded {
									tm.tokenTypes[token] = "dynamic"
								}

								expired := !checkSession(&SessionInfo{FirstAccessAt: time.Unix(tsUnix, 0), ExpireDuration: tm.DynamicConfig.TTL})
								// logger.LogPrintf("动态token验证成功: %s, url: %s, expired: %v", token, urlPath, expired)
								return !expired
							} else {
								logger.LogPrintf("动态token已过期: 时间戳 %s, TTL %s", time.Unix(tsUnix, 0).Format("2006-01-02 15:04:05"), tm.DynamicConfig.TTL)
							}
						} else {
							logger.LogPrintf("动态token时间戳解析失败: %v, 时间戳字符串: %s", err, tsStr)
						}
					} else {
						logger.LogPrintf("动态token salt不匹配: 期望 %s, 实际 %s", tm.DynamicConfig.Salt, salt)
					}
				} else {
					logger.LogPrintf("动态token格式错误: %s", plain)
				}
			} else {
				logger.LogPrintf("动态token解密结果为空")
			}
		}
	}

	// ---------------------------
	// token 验证失败
	// ---------------------------
	logger.LogPrintf("Token验证失败: %s, url: %s, connID: %s", token, urlPath, connID)
	return false
}

// ---------------------------
// 生成动态 token
// ---------------------------
func (tm *TokenManager) GenerateDynamicToken(urlPath string) (string, error) {
	if tm.DynamicConfig == nil {
		return "", errors.New("动态 token 未启用")
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	plain := tm.DynamicConfig.Salt + "|" + urlPath + "|" + timestamp
	return aesEncryptBase64(plain, tm.DynamicConfig.Secret)
}

// ---------------------------
// 清理过期 token
// ---------------------------
func (tm *TokenManager) CleanupExpiredSessions() {
	if !tm.Enabled {
		return
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()

	now := time.Now()

	// 定义检查会话是否过期的函数
	isSessionExpired := func(sess *SessionInfo) bool {
		// 对于静态token，如果没有设置首次访问时间，这是正常的，不应视为过期
		if _, isStatic := tm.StaticTokens[sess.Token]; isStatic {
			// 静态token的过期检查基于ExpireDuration是否为0（0表示永不过期）
			// 如果设置了ExpireDuration，则从首次访问时间计算是否过期
			if sess.ExpireDuration == 0 {
				return false // 永不过期
			}

			// 如果没有设置首次访问时间，说明还未使用，不应视为过期
			if sess.FirstAccessAt.IsZero() {
				return false
			}

			// 判断是否过期：从首次访问时间到现在超过了ExpireDuration
			return now.Sub(sess.FirstAccessAt) > sess.ExpireDuration
		}

		// 对于动态token，必须有首次访问时间
		if sess.FirstAccessAt.IsZero() {
			return true
		}

		// 判断是否过期：从首次访问时间到现在超过了ExpireDuration
		return sess.ExpireDuration > 0 && now.Sub(sess.FirstAccessAt) > sess.ExpireDuration
	}

	// 清理静态token
	for token, sess := range tm.StaticTokens {
		if isSessionExpired(sess) {
			// logger.LogPrintf("清理过期静态token: %s", token)
			delete(tm.StaticTokens, token)
			delete(tm.tokenTypes, token)
			// 从全局状态中也删除
			staticTokenStatesMutex.Lock()
			delete(staticTokenStates, token)
			staticTokenStatesMutex.Unlock()
		}
	}

	// 清理动态token
	for token, sess := range tm.DynamicTokens {
		if isSessionExpired(sess) {
			// 在删除前记录被清理的会话信息
			// logger.LogPrintf("清理过期会话: token=%s, originalURL=%s, ip=%s, expireDuration=%s",
				// sess.Token, sess.OriginalURL, sess.IP, sess.ExpireDuration)
			delete(tm.DynamicTokens, token)
			delete(tm.tokenTypes, token)
		}
	}
}

// ---------------------------
// AES 工具函数 (CBC + PKCS7 + Base64)
// ---------------------------
func aesEncryptBase64(plainText, key string) (string, error) {
	block, err := aes.NewCipher(normalizeKey(key))
	if err != nil {
		return "", err
	}
	plainBytes := pkcs7Padding([]byte(plainText), block.BlockSize())
	cipherText := make([]byte, aes.BlockSize+len(plainBytes))
	iv := cipherText[:aes.BlockSize]
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return "", err
	}
	mode := cipher.NewCBCEncrypter(block, iv)
	mode.CryptBlocks(cipherText[aes.BlockSize:], plainBytes)
	return base64.StdEncoding.EncodeToString(cipherText), nil
}

// AESDecryptBase64 AES解密Base64编码的密文
func AESDecryptBase64(cipherBase64, key string) (string, error) {
	cipherBytes, err := base64.StdEncoding.DecodeString(cipherBase64)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(normalizeKey(key))
	if err != nil {
		return "", err
	}
	if len(cipherBytes) < aes.BlockSize {
		return "", errors.New("cipher too short")
	}
	iv := cipherBytes[:aes.BlockSize]
	cipherBytes = cipherBytes[aes.BlockSize:]
	if len(cipherBytes)%block.BlockSize() != 0 {
		return "", errors.New("cipher is not multiple of block size")
	}
	mode := cipher.NewCBCDecrypter(block, iv)
	mode.CryptBlocks(cipherBytes, cipherBytes)
	plainBytes, err := pkcs7Unpadding(cipherBytes)
	if err != nil {
		return "", err
	}
	return string(plainBytes), nil
}

func aesDecryptBase64(cipherBase64, key string) (string, error) {
	return AESDecryptBase64(cipherBase64, key)
}

// ---------------------------
// PKCS7 Padding
// ---------------------------
func pkcs7Padding(src []byte, blockSize int) []byte {
	pad := blockSize - len(src)%blockSize
	padding := bytesRepeat(byte(pad), pad)
	return append(src, padding...)
}

func pkcs7Unpadding(src []byte) ([]byte, error) {
	length := len(src)
	if length == 0 {
		return nil, errors.New("invalid padding size")
	}
	pad := int(src[length-1])
	if pad > length || pad == 0 {
		return nil, errors.New("invalid padding")
	}
	return src[:length-pad], nil
}

func bytesRepeat(b byte, count int) []byte {
	out := make([]byte, count)
	for i := range out {
		out[i] = b
	}
	return out
}

func normalizeKey(key string) []byte {
	hash := sha256.Sum256([]byte(key))
	return hash[:]
}
