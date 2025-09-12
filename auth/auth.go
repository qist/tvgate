package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/monitor"
)

// 全局Token管理器
var GlobalTokenManager *TokenManager

// GetGlobalTokenManager 获取全局Token管理器
func GetGlobalTokenManager() *TokenManager {
	return GlobalTokenManager
}

// NewGlobalTokenManagerFromConfig 从全局配置创建Token管理器
func NewGlobalTokenManagerFromConfig(globalAuth *config.AuthConfig) *TokenManager {
	domainConfig := &DomainMapConfig{
		Auth: *globalAuth,
	}
	
	return NewTokenManagerFromConfig(domainConfig)
}

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
		tm.StaticTokens[st.Token] = &SessionInfo{
			Token:          st.Token,
			ExpireDuration: st.ExpireHours,
		}
		tm.tokenTypes[st.Token] = "static"
	}

	// 处理动态 token 配置
	if cfg.Auth.DynamicTokens.EnableDynamic {
		tm.DynamicConfig = &DynamicTokenConfig{
			Secret: cfg.Auth.DynamicTokens.Secret,
			Salt:   cfg.Auth.DynamicTokens.Salt,
			TTL:    cfg.Auth.DynamicTokens.DynamicTTL,
		}
	}

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
	if !tm.Enabled {
		return true
	}

	// 检查token参数是否为空
	if token == "" {
		logger.LogPrintf("Token参数为空")
		return false
	}

	tm.mu.RLock()
	defer tm.mu.RUnlock()

	now := time.Now()

	// ---------------------------
	// session 是否过期检查
	// ---------------------------
	checkSession := func(sess *SessionInfo) bool {
		if sess.FirstAccessAt.IsZero() {
			return false
		}

		// 从monitor获取实际的最后活跃时间
		lastActiveAt := sess.FirstAccessAt // 默认使用首次访问时间
		if conn := monitor.ActiveClients.GetConnectionByID(sess.ConnID); conn != nil {
			lastActiveAt = conn.LastActive
		}

		// 判断过期逻辑：从最后活跃时间到现在超过了ExpireDuration
		if sess.ExpireDuration > 0 && now.Sub(lastActiveAt) > sess.ExpireDuration {
			return false // 已过期
		}
		
		return true // 未过期
	}

	// ---------------------------
	// 检查token类型（如果已记录）
	// ---------------------------
	tokenType, typeRecorded := tm.tokenTypes[token]

	// ---------------------------
	// 如果记录为静态token或未记录类型，尝试验证静态 token
	// ---------------------------
	if !typeRecorded || tokenType == "static" {
		if tm.StaticTokens != nil {
			if sess, ok := tm.StaticTokens[token]; ok {
				// 记录token类型
				if !typeRecorded {
					tm.tokenTypes[token] = "static"
				}
				
				tm.mu.RUnlock()
				tm.mu.Lock()
				// 确保FirstAccessAt只在第一次访问时设置
				if sess.FirstAccessAt.IsZero() {
					sess.FirstAccessAt = now
					sess.OriginalURL = urlPath // 记录首次访问的URL
					if idx := strings.Index(connID, "_"); idx != -1 {
						sess.IP = connID[:idx] // 提取IP部分
					}
				} else {
					// 检查是否是同一IP访问
					clientIP := connID
					if idx := strings.Index(connID, "_"); idx != -1 {
						clientIP = connID[:idx]
					}
					
					if sess.IP != clientIP {
						// 不同IP访问，拒绝
						tm.mu.Unlock()
						tm.mu.RLock()
						logger.LogPrintf("静态token访问被拒绝: 不同IP尝试访问, 原始IP=%s, 当前IP=%s, URL=%s", sess.IP, clientIP, urlPath)
						return false
					}
				}
				sess.ConnID = connID
				tm.mu.Unlock()
				tm.mu.RLock()

				expired := !checkSession(sess)
				// 从monitor获取实际的最后活跃时间用于日志输出
				lastActiveAt := sess.FirstAccessAt
				if conn := monitor.ActiveClients.GetConnectionByID(sess.ConnID); conn != nil {
					lastActiveAt = conn.LastActive
				}
				
				logger.LogPrintf(
					"静态token验证成功: %s, url: %s, originalURL: %s, ip: %s, connID: %s, ExpireDuration: %s, FirstAccessAt: %s, LastActiveAt: %s, expired: %v",
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
			}
		}
	}

	// ---------------------------
	// 如果记录为动态token或未记录类型，尝试验证动态 token
	// ---------------------------
	if (!typeRecorded || tokenType == "dynamic") && tm.DynamicConfig != nil {
		// 记录token参数用于调试
		logger.LogPrintf("尝试验证动态token: %s", token)
		
		// 修复URL解码过程中"+"被替换为空格的问题
		fixedToken := token
		if strings.Contains(token, " ") {
			fixedToken = strings.ReplaceAll(token, " ", "+")
			logger.LogPrintf("修复URL解码问题: 原token包含空格，已替换为+")
		}
		
		// 尝试解密
		plain, err := aesDecryptBase64(fixedToken, tm.DynamicConfig.Secret)
		if err != nil {
			logger.LogPrintf("动态token解密失败: %v, token长度: %d, token内容: %s", err, len(token), token)
			// 如果修复后的token仍然失败，记录详细信息
			if fixedToken != token {
				logger.LogPrintf("修复后的token也失败，原始token: %s, 修复后token: %s", token, fixedToken)
			}
		} else {
			// 检查解密结果
			if plain != "" {
				parts := strings.SplitN(plain, "|", 3)
				if len(parts) == 3 {
					salt, _, tsStr := parts[0], parts[1], parts[2]
					if salt == tm.DynamicConfig.Salt {
						// 不再检查路径是否匹配，始终使用token中存储的原始路径
						// 这样确保了第一次生成的路径一直有效，适用于301重定向、TS文件等场景
						
						tsUnix, err := strconv.ParseInt(tsStr, 10, 64)
						if err == nil {
							if tm.DynamicConfig.TTL <= 0 || time.Since(time.Unix(tsUnix, 0)) <= tm.DynamicConfig.TTL {
								// 动态token验证成功，存入动态 token 会话
								if !typeRecorded {
									tm.tokenTypes[token] = "dynamic"
								}
								
								tm.mu.RUnlock()
								tm.mu.Lock()
								var sess *SessionInfo
								if existingSess, exists := tm.DynamicTokens[token]; exists {
									sess = existingSess
									
									// 确保FirstAccessAt只在第一次访问时设置
									if sess.FirstAccessAt.IsZero() {
										sess.FirstAccessAt = now
										sess.OriginalURL = urlPath // 记录首次访问的URL
										if idx := strings.Index(connID, "_"); idx != -1 {
											sess.IP = connID[:idx] // 提取IP部分
										}
									} else {
										// 检查是否是同一IP访问同一URL或相关资源
										clientIP := connID
										if idx := strings.Index(connID, "_"); idx != -1 {
											clientIP = connID[:idx]
										}
										
										if sess.IP != clientIP {
											// 不同IP访问，拒绝
											tm.mu.Unlock()
											logger.LogPrintf("动态token访问被拒绝: 不同IP尝试访问, 原始IP=%s, 当前IP=%s, URL=%s", sess.IP, clientIP, urlPath)
											return false
										}
										
										// 检查URL是否匹配或为相关资源
										// 允许访问相同目录结构下的不同资源
										// 例如: /PLTV/88888888/224/3221236260/index.m3u8 和 /PLTV/88888888/224/3221236335/index.m3u8
										if sess.OriginalURL != urlPath {
											// 检查是否为同一类型的资源（例如都是m3u8文件）
											originalExt := filepath.Ext(sess.OriginalURL)
											currentExt := filepath.Ext(urlPath)
											
											// 如果扩展名不同，拒绝访问
											if originalExt != currentExt {
												// 不同URL且不是相关资源，拒绝
												tm.mu.Unlock()
												logger.LogPrintf("动态token访问被拒绝: IP尝试访问不同类型的资源, IP=%s, 原始URL=%s, 当前URL=%s", sess.IP, sess.OriginalURL, urlPath)
												return false
											}
											
											// 检查是否为同一目录结构下的资源
											originalDir := filepath.Dir(sess.OriginalURL)
											currentDir := filepath.Dir(urlPath)
											
											// 如果目录结构不同，拒绝访问
											if originalDir != currentDir {
												// 检查是否为相似的目录结构（例如都是/PLTV/数字/数字/数字/格式）
												originalDirParts := strings.Split(strings.Trim(originalDir, "/"), "/")
												currentDirParts := strings.Split(strings.Trim(currentDir, "/"), "/")
												
												// 检查目录层级是否相同且前几级目录是否一致
												if len(originalDirParts) != len(currentDirParts) {
													tm.mu.Unlock()
													logger.LogPrintf("动态token访问被拒绝: IP尝试访问不同目录结构的资源, IP=%s, 原始目录=%s, 当前目录=%s", sess.IP, originalDir, currentDir)
													return false
												}
												
												// 检查前几级目录是否一致（允许最后一级数字不同）
												if len(originalDirParts) >= 4 {
													matching := true
													for i := 0; i < len(originalDirParts)-1; i++ {
														if originalDirParts[i] != currentDirParts[i] {
															matching = false
															break
														}
													}
													
													// 如果前几级目录不一致，拒绝访问
													if !matching {
														tm.mu.Unlock()
														logger.LogPrintf("动态token访问被拒绝: IP尝试访问不同目录结构的资源, IP=%s, 原始目录=%s, 当前目录=%s", sess.IP, originalDir, currentDir)
														return false
													}
												} else {
													// 目录层级太少，进行严格匹配
													tm.mu.Unlock()
													logger.LogPrintf("动态token访问被拒绝: IP尝试访问不同目录结构的资源, IP=%s, 原始目录=%s, 当前目录=%s", sess.IP, originalDir, currentDir)
													return false
												}
											}
										}
									}
									sess.ConnID = connID
								} else {
									ip := connID
									if idx := strings.Index(connID, "_"); idx != -1 {
										ip = connID[:idx]
									}
									
									sess = &SessionInfo{
										Token:          token,
										ConnID:         connID,
										FirstAccessAt:  now,
										ExpireDuration: tm.DynamicConfig.TTL,
										OriginalURL:    urlPath, // 记录首次访问的URL
										IP:             ip, // 提取IP部分
									}
									tm.DynamicTokens[token] = sess
								}
								tm.mu.Unlock()
								tm.mu.RLock()

								expired := !checkSession(sess)
								// 从monitor获取实际的最后活跃时间用于日志输出
								lastActiveAt := sess.FirstAccessAt
								if conn := monitor.ActiveClients.GetConnectionByID(sess.ConnID); conn != nil {
									lastActiveAt = conn.LastActive
								}
								
								logger.LogPrintf(
									"动态token验证成功: %s, url: %s, originalURL: %s, ip: %s, connID: %s, ExpireDuration: %s, FirstAccessAt: %s, LastActiveAt: %s, expired: %v",
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
		// 如果没有设置首次访问时间，认为会话无效
		if sess.FirstAccessAt.IsZero() {
			return true
		}
		
		// 判断是否过期：从首次访问时间到现在超过了ExpireDuration
		return sess.ExpireDuration > 0 && now.Sub(sess.FirstAccessAt) > sess.ExpireDuration
	}

	// 清理静态token
	for token, sess := range tm.StaticTokens {
		if isSessionExpired(sess) {
			delete(tm.StaticTokens, token)
		}
	}
	
	// 清理动态token
	for token, sess := range tm.DynamicTokens {
		if isSessionExpired(sess) {
			// 在删除前记录被清理的会话信息
			logger.LogPrintf("清理过期会话: token=%s, originalURL=%s, ip=%s, expireDuration=%s", 
				sess.Token, sess.OriginalURL, sess.IP, sess.ExpireDuration)
			delete(tm.DynamicTokens, token)
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
