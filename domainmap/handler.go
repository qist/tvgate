package domainmap

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qist/tvgate/auth"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/dns"
	"github.com/qist/tvgate/lb"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/monitor"
	"github.com/qist/tvgate/proxy"
	"github.com/qist/tvgate/rules"
	"github.com/qist/tvgate/stream"
	"github.com/qist/tvgate/utils/buffer"
)

// ---------------------------
// 工具函数
// ---------------------------

func getRequestScheme(r *http.Request) string {
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		return proto
	}
	if cfVisitor := r.Header.Get("CF-Visitor"); strings.Contains(cfVisitor, "https") {
		return "https"
	}
	if forwarded := r.Header.Get("Forwarded"); strings.Contains(forwarded, "proto=https") {
		return "https"
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func chooseScheme(protocol string, r *http.Request) string {
	if protocol != "" {
		return protocol
	}
	if r != nil {
		return getRequestScheme(r)
	}
	return "http"
}

// ---------------------------
// 核心结构
// ---------------------------

type DomainMapper struct {
	mappings        auth.DomainMapList
	client          *http.Client
	next            http.Handler
	redirectHandler *RedirectHandler
	tokenManagers   map[string]*auth.TokenManager
	realHostMap     map[string]string // 真实地址映射表
	realHostMapMu   sync.RWMutex      // 保护realHostMap的互斥锁
}

type RedirectHandler struct {
	domainMapper *DomainMapper
	maxRedirects int
}

// ---------------------------
// 初始化
// ---------------------------

func NewDomainMapper(mappings auth.DomainMapList, client *http.Client, next http.Handler) *DomainMapper {
	if client == nil {
		client = &http.Client{
			Timeout: 30 * time.Second,
		}
	}

	dm := &DomainMapper{
		mappings:      mappings,
		client:        client,
		next:          next,
		tokenManagers: make(map[string]*auth.TokenManager),
		realHostMap:   make(map[string]string), // 初始化真实地址映射表
	}

	// 初始化时清理tokenManagers
	dm.CleanTokenManagers()

	// 启动缓存清理器
	StartCacheCleaner()

	dm.redirectHandler = &RedirectHandler{
		domainMapper: dm,
		maxRedirects: 10,
	}

	client.CheckRedirect = dm.redirectHandler.redirectPolicy
	return dm
}

// ---------------------------
// 域名匹配
// ---------------------------

func (dm *DomainMapper) MapDomain(host string) (string, string, bool) {
	hostWithoutPort := host
	if idx := strings.Index(host, ":"); idx != -1 {
		hostWithoutPort = host[:idx]
	}
	for _, mapping := range dm.mappings {
		if mapping.Source == hostWithoutPort {
			return mapping.Target, mapping.Protocol, true
		}
	}
	return "", "", false
}

func (dm *DomainMapper) GetDomainConfig(host string) *auth.DomainMapConfig {
	hostWithoutPort := host
	if idx := strings.Index(host, ":"); idx != -1 {
		hostWithoutPort = host[:idx]
	}
	for _, mapping := range dm.mappings {
		if mapping.Source == hostWithoutPort {
			return mapping
		}
	}
	return nil
}

// ReverseMapDomain 反向映射域名，将target转换为source
func (dm *DomainMapper) ReverseMapDomain(targetHost string) (string, string, bool) {
	for _, mapping := range dm.mappings {
		if mapping.Target == targetHost {
			return mapping.Source, mapping.Protocol, true
		}
	}
	return "", "", false
}

// ShouldReplaceURL 检查是否应该替换URL
func (dm *DomainMapper) ShouldReplaceURL(host string) bool {
	_, _, found := dm.MapDomain(host)
	return found
}

// GetOriginalHost 获取原始主机名
func (dm *DomainMapper) GetOriginalHost(frontendHost string) string {
	originalHost, _, found := dm.ReverseMapDomain(frontendHost)
	if found {
		return originalHost
	}
	return frontendHost
}

// ---------------------------
// 重定向策略
// ---------------------------

func (rh *RedirectHandler) redirectPolicy(req *http.Request, via []*http.Request) error {
	if len(via) >= rh.maxRedirects {
		return http.ErrUseLastResponse
	}

	originalHost := ""
	if len(via) > 0 {
		originalHost = via[0].Host
	}

	targetHost, protocol, found := rh.domainMapper.MapDomain(req.Host)
	if found {
		if protocol != "" {
			req.URL.Scheme = protocol
		}
		req.URL.Host = targetHost
		req.Host = targetHost
		req.Header.Set("Host", targetHost)
	} else if originalHost != "" && originalHost != req.Host {
		origTargetHost, origProtocol, origFound := rh.domainMapper.MapDomain(originalHost)
		if origFound {
			if origProtocol != "" {
				req.URL.Scheme = origProtocol
			}
			req.URL.Host = origTargetHost
			req.Host = origTargetHost
			req.Header.Set("Host", origTargetHost)
		}
	}
	return nil
}

// ---------------------------
// URL 替换 + Token
// ---------------------------

func (dm *DomainMapper) replaceSpecialNestedURL(parsedURL *url.URL, frontendScheme, frontendHost string, tm *auth.TokenManager, tokenParam string) (string, bool, string) {
	originalHost := parsedURL.Host
	innerPath := parsedURL.Path

	// 去掉嵌套的 host 前缀
	if strings.HasPrefix(innerPath, "/"+originalHost) {
		innerPath = strings.TrimPrefix(innerPath, "/"+originalHost)
		if !strings.HasPrefix(innerPath, "/") {
			innerPath = "/" + innerPath
		}
	}

	// 构造新 URL（不改变查询参数）用于前端显示
	// 始终使用传入的 frontendScheme 和 frontendHost 作为前端显示地址
	newURL := &url.URL{
		Scheme:   frontendScheme,
		Host:     frontendHost,
		Path:     innerPath,
		RawQuery: parsedURL.RawQuery, // 保留原始 query，不 encode
		Fragment: parsedURL.Fragment,
	}

	var newToken string

	// 添加 token
	if tm != nil && tm.Enabled {
		// tokenParam := tm.TokenParamName
		// if tokenParam == "" {
		// 	tokenParam = "token"
		// }

		// 动态 token
		if tm.DynamicConfig != nil {
			if tok, err := tm.GenerateDynamicToken(innerPath); err == nil {
				newToken = tok
			}
		}

		// 静态 token（仅在动态 token 为空时使用）
		if newToken == "" && len(tm.StaticTokens) > 0 {
			for st := range tm.StaticTokens {
				newToken = st
				break
			}
		}

		// 如果生成了 token，则手动拼接到原始 query
		if newToken != "" {
			if newURL.RawQuery == "" {
				newURL.RawQuery = tokenParam + "=" + newToken
			} else {
				// 保持原始 query，不 encode
				newURL.RawQuery += "&" + tokenParam + "=" + newToken
			}
		}
	} else {
		// 如果没有特定的token管理器，检查全局授权
		if globalTm := auth.GetGlobalTokenManager(); globalTm != nil && globalTm.Enabled {

			// tokenParam := globalTm.TokenParamName

			// if tokenParam == "" {
			// 	tokenParam = "token"
			// }

			// 优先尝试生成动态token
			if globalTm.DynamicConfig != nil {
				if tok, err := globalTm.GenerateDynamicToken(innerPath); err == nil {
					newToken = tok
				}
			}

			// 如果动态token生成失败，尝试使用静态token
			if newToken == "" && len(globalTm.StaticTokens) > 0 {
				for st := range globalTm.StaticTokens {
					newToken = st
					break
				}
			}

			// 如果生成了 token，则手动拼接到原始 query
			if newToken != "" {
				if newURL.RawQuery == "" {
					newURL.RawQuery = tokenParam + "=" + newToken
				} else {
					// 保持原始 query，不 encode
					newURL.RawQuery += "&" + tokenParam + "=" + newToken
				}
			}
		}
	}

	// 判断是否替换了 host
	replaced := newURL.Scheme != parsedURL.Scheme || newURL.Host != parsedURL.Host || newURL.Path != parsedURL.Path

	return newURL.String(), replaced, newToken
}

// ---------------------------
// Token 管理器清理
// ---------------------------

// CleanTokenManagers 清理domainmap中不再使用的token管理器
// 当配置重新加载后，某些域名映射可能已被移除，需要清理对应的token管理器
func (dm *DomainMapper) CleanTokenManagers() {
	if dm.tokenManagers == nil {
		return
	}

	// 遍历 tokenManagers，清理不再需要的条目
	for host, tm := range dm.tokenManagers {
		if tm == nil {
			continue
		}

		// 检查该 host 是否还在当前的域名映射配置中
		if cfg := dm.GetDomainConfig(host); cfg == nil {
			// 如果不在配置中，清理这个 tokenManager
			// 调用CleanupExpiredSessions方法清理过期会话
			tm.CleanupExpiredSessions()
			delete(dm.tokenManagers, host)
		}
	}
}

// AddRealHostMapping 添加真实地址映射
func (dm *DomainMapper) AddRealHostMapping(realHost, sourceHost string) {
	dm.realHostMapMu.Lock()
	defer dm.realHostMapMu.Unlock()

	// logger.LogPrintf("DEBUG: 添加映射关系到缓存 - RealHost: %s -> SourceHost: %s", realHost, sourceHost)
	dm.realHostMap[realHost] = sourceHost
}

// GetSourceHost 获取source地址
func (dm *DomainMapper) GetSourceHost(realHost string) (string, bool) {
	dm.realHostMapMu.RLock()
	defer dm.realHostMapMu.RUnlock()

	sourceHost, exists := dm.realHostMap[realHost]
	// logger.LogPrintf("DEBUG: 查找映射关系 - RealHost: %s, Found: %t, SourceHost: %s", realHost, exists, sourceHost)
	return sourceHost, exists
}

// RemoveRealHostMapping 移除真实地址映射
func (dm *DomainMapper) RemoveRealHostMapping(realHost string) {
	dm.realHostMapMu.Lock()
	defer dm.realHostMapMu.Unlock()
	delete(dm.realHostMap, realHost)
}

// ---------------------------
// M3U8 处理辅助函数
// ---------------------------

func (dm *DomainMapper) replaceSpecialNestedURLClean(
	line string,
	frontendScheme, frontendHost, sourceHost string,
	tm *auth.TokenManager,
	tokenParam string,
	seen map[string]struct{},
) []byte {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return nil
	}

	// ---------- 处理 # 开头的标签 ----------
	if strings.HasPrefix(trimmed, "#") {
		// 检查是否包含 URI=
		if strings.Contains(trimmed, "URI=\"") {
			re := regexp.MustCompile(`URI="([^"]+)"`)
			trimmed = re.ReplaceAllStringFunc(trimmed, func(match string) string {
				uri := re.FindStringSubmatch(match)[1]
				newURI := uri

				// 生成 token
				token := ""
				if tm != nil && tm.Enabled {
					if tm.DynamicConfig != nil {
						if tok, err := tm.GenerateDynamicToken(uri); err == nil {
							token = tok
						}
					}
					if token == "" && len(tm.StaticTokens) > 0 {
						for st := range tm.StaticTokens {
							token = st
							break
						}
					}
				} else {
					// 如果没有特定的token管理器，检查全局授权
					if globalTm := auth.GetGlobalTokenManager(); globalTm != nil && globalTm.Enabled {
						// 优先尝试生成动态token
						if globalTm.DynamicConfig != nil {
							if tok, err := globalTm.GenerateDynamicToken(uri); err == nil {
								token = tok
							}
						}

						// 如果动态token生成失败，尝试使用静态token
						if token == "" && len(globalTm.StaticTokens) > 0 {
							for st := range globalTm.StaticTokens {
								token = st
								break
							}
						}
					}
				}

				// 添加 token
				if token != "" {
					if strings.Contains(newURI, "?") {
						newURI += "&" + tokenParam + "=" + token
					} else {
						newURI += "?" + tokenParam + "=" + token
					}
				}
				return fmt.Sprintf(`URI="%s"`, newURI)
			})
		}
		return []byte(trimmed + "\n")
	}

	// ---------- 普通分片/子 m3u8 ----------
	newLine := trimmed

	// token
	token := ""
	if tm != nil && tm.Enabled {
		if tm.DynamicConfig != nil {
			if tok, err := tm.GenerateDynamicToken(trimmed); err == nil {
				token = tok
			}
		}
		if token == "" && len(tm.StaticTokens) > 0 {
			for st := range tm.StaticTokens {
				token = st
				break
			}
		}
	} else {
		// 如果没有特定的token管理器，检查全局授权
		if globalTm := auth.GetGlobalTokenManager(); globalTm != nil && globalTm.Enabled {
			// 优先尝试生成动态token
			if globalTm.DynamicConfig != nil {
				if tok, err := globalTm.GenerateDynamicToken(trimmed); err == nil {
					token = tok
				}
			}

			// 如果动态token生成失败，尝试使用静态token
			if token == "" && len(globalTm.StaticTokens) > 0 {
				for st := range globalTm.StaticTokens {
					token = st
					break
				}
			}
		}
	}

	if strings.HasPrefix(newLine, "http://") || strings.HasPrefix(newLine, "https://") {
		// 完整 URL，替换 host 并加 token
		u, err := url.Parse(newLine)
		if err == nil {
			// 记录原始URL信息用于调试
			// originalHost := u.Host
			// logger.LogPrintf("DEBUG: 处理URL: %s, 原始Host: %s", newLine, originalHost)

			// 前端显示始终为 source 地址（如 192.168.0.151:8888）
			u.Host = sourceHost
			// logger.LogPrintf("DEBUG: 替换Host: %s -> %s", originalHost, u.Host)

			u.Scheme = frontendScheme

			// 清理重复 host
			parts := strings.Split(u.Host+u.Path, "/")
			cleanedParts := []string{}
			for _, p := range parts {
				if p == "" {
					continue // 可以选择是否过滤掉空的
				}
				cleanedParts = append(cleanedParts, p)
			}

			// 确保端口和路径之间有正确的分隔
			if len(cleanedParts) > 1 {
				// 检查是否端口和路径之间缺少斜杠
				hostPart := cleanedParts[0]
				pathPart := strings.Join(cleanedParts[1:], "/")
				if !strings.HasSuffix(hostPart, "/") && !strings.HasPrefix(pathPart, "/") {
					u.Path = "/" + pathPart
				} else {
					u.Path = pathPart
				}
			} else {
				u.Path = ""
			}

			// 确保Host部分正确（不包含路径）
			u.Host = cleanedParts[0]

			// 添加 token（不 encode）
			if token != "" {
				q := u.Query()
				q.Set(tokenParam, token)
				rawQueryParts := []string{}
				for k, vals := range q {
					for _, v := range vals {
						rawQueryParts = append(rawQueryParts, k+"="+v)
					}
				}
				if len(rawQueryParts) > 0 {
					u.RawQuery = strings.Join(rawQueryParts, "&")
				} else {
					u.RawQuery = ""
				}
			}

			newLine = u.Scheme + "://" + u.Host + u.Path
			if u.RawQuery != "" {
				newLine += "?" + u.RawQuery
			}
			// logger.LogPrintf("DEBUG: 最终URL: %s", newLine)
		}
	} else {
		// 相对路径，只加 token
		if token != "" {
			if strings.Contains(newLine, "?") {
				newLine += "&" + tokenParam + "=" + token
			} else {
				newLine += "?" + tokenParam + "=" + token
			}
		}
	}

	seen[newLine] = struct{}{}
	return []byte(newLine + "\n")
}

// ---------------------------
// HTTP 转发
// ---------------------------

func (dm *DomainMapper) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer dm.CleanTokenManagers()
	targetHost, protocol, found := dm.MapDomain(r.Host)
	// logger.LogPrintf("映射域名: %s -> %s", r.Host, targetHost)
	if !found {
		dm.next.ServeHTTP(w, r)
		return
	}
	cfg := dm.GetDomainConfig(r.Host)
	if cfg == nil {
		dm.next.ServeHTTP(w, r)
		return
	}

	// 获取sourceHost（即r.Host对应的source地址）
	sourceHost := r.Host

	// 打印调试信息
	// logger.LogPrintf("DEBUG: 请求处理开始 - Host: %s, TargetHost: %s, Protocol: %s, SourceHost: %s",
	// r.Host, targetHost, protocol, sourceHost)

	// 确定使用哪个token参数名和token值
	tokenParam := ""
	token := ""
	clientIP := ""
	connID := ""
	// 优先使用domainmap本地配置，没有配置才是全局认证配置
	if cfg.Auth.TokensEnabled && (cfg.Auth.DynamicTokens.EnableDynamic || cfg.Auth.StaticTokens.EnableStatic) {
		// 使用domainmap本地配置
		tokenParam = cfg.Auth.TokenParamName
		if tokenParam == "" {
			tokenParam = "token"
		}
		token = r.URL.Query().Get(tokenParam)
	} else if globalTm := auth.GetGlobalTokenManager(); globalTm != nil && globalTm.Enabled && (globalTm.DynamicConfig != nil || len(globalTm.StaticTokens) > 0) {
		// 使用全局token管理器的参数名
		tokenParam = globalTm.TokenParamName
		if tokenParam == "" {
			tokenParam = "my_token" // 全局默认参数名
		}
		token = r.URL.Query().Get(tokenParam)
	} else {
		// 默认情况，尝试获取token但不进行验证
		// 先尝试本地参数名
		tokenParam = cfg.Auth.TokenParamName
		if tokenParam == "" {
			// 再尝试全局参数名
			if globalTm := auth.GetGlobalTokenManager(); globalTm != nil {
				tokenParam = globalTm.TokenParamName
				if tokenParam == "" {
					tokenParam = "my_token"
				}
			} else {
				tokenParam = "token"
			}
		}
		token = r.URL.Query().Get(tokenParam)
	}
	if protocol == "rtsp" {
		// 统一认证逻辑
		clientIP := monitor.GetClientIP(r)
		connID = clientIP + "_" + strconv.FormatInt(time.Now().UnixNano(), 10)

	} else {
		clientIP := monitor.GetClientIP(r)
		frontendScheme := getRequestScheme(r)
		// targetURL 是用于前端显示的URL（始终显示为source地址）
		targetURL := &url.URL{
			Scheme: frontendScheme,
			Host:   sourceHost, // 使用source地址作为前端显示地址
		}
		hash := md5.Sum([]byte(fmt.Sprintf("%s://%s", targetURL.Scheme, targetURL.Host)))
		connID = clientIP + "_" + hex.EncodeToString(hash[:])
	}
	// 校验 client headers
	if len(cfg.ClientHeaders) > 0 {
		for k, v := range cfg.ClientHeaders {
			if r.Header.Get(k) != v {
				http.Error(w, "Forbidden", http.StatusUnauthorized)
				return
			}
		}
	}
	// 验证token - 统一处理HTTP和RTSP
	if cfg.Auth.TokensEnabled {
		// 如果domainmap配置了授权且启用了tokens，则进行验证
		if cfg.Auth.DynamicTokens.EnableDynamic || cfg.Auth.StaticTokens.EnableStatic {
			var tm *auth.TokenManager

			// 使用host作为key来获取TokenManager
			host := r.Host
			if existingTm, ok := dm.tokenManagers[host]; ok {
				tm = existingTm
			} else {
				tm = auth.NewTokenManagerFromConfig(cfg)
				dm.tokenManagers[host] = tm
			}

			// 验证token
			if !tm.ValidateToken(token, r.URL.Path, connID) {
				http.Error(w, "Forbidden", http.StatusUnauthorized)
				return
			}

			// 更新token活跃状态
			tm.KeepAlive(token, connID, clientIP, r.URL.Path)
		}
	} else {
		// 2. 只有当domainmap没有开启授权时，才检查全局认证
		if globalTm := auth.GetGlobalTokenManager(); globalTm != nil && globalTm.Enabled {
			// 如果全局认证启用且配置了动态或静态token，则进行验证
			if globalTm.DynamicConfig != nil || len(globalTm.StaticTokens) > 0 {
				// 验证token
				if !globalTm.ValidateToken(token, r.URL.Path, connID) {
					http.Error(w, "Forbidden", http.StatusUnauthorized)
					return
				}

				// 更新token活跃状态
				globalTm.KeepAlive(token, connID, clientIP, r.URL.Path)
			}
		}
		// 3. 如果都没有配置授权，则不进行认证
	}

	// 如果协议是RTSP，则转发给RTSP处理器
	if protocol == "rtsp" {
		// 构造新的URL路径，格式为 /rtsp/{targetHost}{原始路径}
		newPath := "/rtsp/" + targetHost + r.URL.Path

		// 创建新的请求
		newReq := r.Clone(r.Context())
		newReq.URL.Path = newPath

		// 处理查询参数，移除token参数
		if r.URL.RawQuery != "" {
			tokenParamToRemove := "my_token"
			// 优先使用domainmap本地配置
			if cfg.Auth.TokenParamName != "" {
				tokenParamToRemove = cfg.Auth.TokenParamName
			} else if globalTm := auth.GetGlobalTokenManager(); globalTm != nil && globalTm.TokenParamName != "" {
				tokenParamToRemove = globalTm.TokenParamName
			}

			// 去掉 token
			query := r.URL.RawQuery
			newParts := []string{}
			for _, kv := range strings.Split(query, "&") {
				if !strings.HasPrefix(kv, tokenParamToRemove+"=") {
					newParts = append(newParts, kv)
				}
			}
			if len(newParts) > 0 {
				newReq.URL.RawQuery = strings.Join(newParts, "&")
			}
		} else {
			newReq.URL.RawQuery = r.URL.RawQuery
		}
		// newReq.URL.RawQuery = r.URL.RawQuery
		// logger.LogPrintf("RTSP → HTTP request: %s", newReq.URL.String())
		// logger.LogPrintf("RTSP → HTTP request: %s", connID)
		// 转发给下一个处理器（即RTSP处理器）
		RtspToHTTPHandler(w, newReq, connID)
		return
	}

	// 获取前端显示用的 scheme (http 或 https)
	frontendScheme := getRequestScheme(r)

	// 检查是否在真实地址映射表中存在映射关系
	// 如果存在，则使用映射表中的target地址进行后端请求
	backendHost := targetHost
	if mappedHost, exists := dm.GetSourceHost(sourceHost); exists {
		backendHost = mappedHost
		// logger.LogPrintf("DEBUG: 找到映射关系 - SourceHost: %s -> BackendHost: %s", sourceHost, backendHost)
	} else {
		// logger.LogPrintf("DEBUG: 未找到映射关系，使用默认TargetHost: %s", targetHost)
	}

	// targetURL 是用于前端显示的URL（始终显示为source地址）
	targetURL := &url.URL{
		Scheme:   frontendScheme,
		Host:     sourceHost, // 使用source地址作为前端显示地址
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
	}

	// originalURL 是用于后端请求的真实URL（使用target地址或映射后的地址）
	originalURL := &url.URL{
		Scheme:   chooseScheme(protocol, r),
		Host:     backendHost, // 使用映射后的target地址或默认target地址
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
	}

	// 记录真实地址映射 (source地址 -> target地址)
	dm.AddRealHostMapping(sourceHost, targetHost)
	// logger.LogPrintf("DEBUG: 添加映射关系 - SourceHost: %s -> TargetHost: %s", sourceHost, targetHost)

	// 打印URL信息
	// logger.LogPrintf("DEBUG: TargetURL (前端显示): %s", targetURL.String())
	// logger.LogPrintf("DEBUG: OriginalURL (后端请求): %s", originalURL.String())

	var tm *auth.TokenManager

	if cfg.Auth.TokensEnabled && (cfg.Auth.DynamicTokens.EnableDynamic || cfg.Auth.StaticTokens.EnableStatic) {
		// 检查是否启用了静态token但没有提供token参数
		if cfg.Auth.StaticTokens.EnableStatic && !cfg.Auth.DynamicTokens.EnableDynamic && token == "" {
			// 如果只启用了静态token但没有提供token参数，则拒绝访问
			http.Error(w, "Forbidden", http.StatusUnauthorized)
			return
		}

		// 使用host作为key来获取TokenManager
		host := r.Host
		if existingTm, ok := dm.tokenManagers[host]; ok {
			tm = existingTm
		} else {
			tm = auth.NewTokenManagerFromConfig(cfg)
			dm.tokenManagers[host] = tm
		}
	}
	// logger.LogPrintf("connID: %s", connID)
	monitor.ActiveClients.Register(connID, &monitor.ClientConnection{
		IP:             clientIP,
		URL:            targetURL.String(),
		UserAgent:      r.UserAgent(),
		ConnectionType: strings.ToUpper(targetURL.Scheme),
		ConnectedAt:    time.Now(),
		LastActive:     time.Now(),
	})
	defer monitor.ActiveClients.Unregister(connID, strings.ToUpper(targetURL.Scheme))
	updateActive := func() {
		monitor.ActiveClients.UpdateLastActive(connID, time.Now())
	}

	// 读取请求体（如果有的话）
	var reqBodyBytes []byte
	if r.Body != nil {
		reqBodyBytes, _ = io.ReadAll(r.Body)
	}

	// 创建一个副本用于实际的 HTTP 请求，避免影响原始的 originalURL 变量
	originalReqURL := *originalURL
	if cfg.Auth.TokensEnabled {
		q := originalReqURL.Query()
		q.Del(tokenParam)

		// 直接构建 RawQuery，不使用 Encode()，保持原始格式
		rawQueryParts := []string{}
		for k, vals := range q {
			for _, v := range vals {
				rawQueryParts = append(rawQueryParts, k+"="+v)
			}
		}
		if len(rawQueryParts) > 0 {
			originalReqURL.RawQuery = strings.Join(rawQueryParts, "&")
		} else {
			originalReqURL.RawQuery = ""
		}
	} else {
		// 使用全局认证配置处理token参数
		if globalTm := auth.GetGlobalTokenManager(); globalTm != nil && globalTm.Enabled {
			// 如果全局认证启用且配置了动态或静态token，则移除token参数
			if globalTm.DynamicConfig != nil || len(globalTm.StaticTokens) > 0 {
				q := originalReqURL.Query()
				tokenParamToRemove := "my_token"
				// 优先使用domainmap本地配置
				if cfg.Auth.TokenParamName != "" {
					tokenParamToRemove = cfg.Auth.TokenParamName
				} else if globalTm.TokenParamName != "" {
					tokenParamToRemove = globalTm.TokenParamName
				}
				q.Del(tokenParamToRemove)

				// 直接构建 RawQuery，不使用 Encode()，保持原始格式
				rawQueryParts := []string{}
				for k, vals := range q {
					for _, v := range vals {
						rawQueryParts = append(rawQueryParts, k+"="+v)
					}
				}
				if len(rawQueryParts) > 0 {
					originalReqURL.RawQuery = strings.Join(rawQueryParts, "&")
				} else {
					originalReqURL.RawQuery = ""
				}
			}
		}
	}

	// -------- 使用动态 HTTP 配置创建 Transport 和 Client ----------
	httpCfg := config.Cfg.HTTP
	config.Cfg.SetDefaults()
	resolver := dns.GetInstance()

	dialer := &net.Dialer{
		Timeout:   httpCfg.ConnectTimeout,
		KeepAlive: httpCfg.KeepAlive,
	}

	// 自定义 DialContext 支持自定义 DNS + 回落
	dialContext := func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}

		// 使用自定义 Resolver 解析
		ips, err := resolver.LookupIPAddr(ctx, host)
		if err != nil || len(ips) == 0 {
			// logger.LogPrintf("⚠️ DNS解析失败 %s: %v, 回落系统DNS", host, err)
			return dialer.DialContext(ctx, network, addr)
		}

		ip := ips[0].IP.String()
		target := net.JoinHostPort(ip, port)
		// logger.LogPrintf("✅ 自定义DNS解析 %s -> %s", host, ip)

		return dialer.DialContext(ctx, network, target)
	}

	// 创建 Transport
	baseTransport := &http.Transport{
		DialContext:           dialContext,
		TLSHandshakeTimeout:   httpCfg.TLSHandshakeTimeout,
		ResponseHeaderTimeout: httpCfg.ResponseHeaderTimeout,
		ExpectContinueTimeout: httpCfg.ExpectContinueTimeout,
		IdleConnTimeout:       httpCfg.IdleConnTimeout,
		MaxIdleConns:          httpCfg.MaxIdleConns,
		MaxIdleConnsPerHost:   httpCfg.MaxIdleConnsPerHost,
		MaxConnsPerHost:       httpCfg.MaxConnsPerHost,
		DisableKeepAlives:     *httpCfg.DisableKeepAlives,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: *httpCfg.InsecureSkipVerify},
	}

	// 创建 Client
	client := &http.Client{
		Transport: baseTransport,
		Timeout:   httpCfg.Timeout,
	}

	// ---------- 处理代理或直连 ----------
	pg := rules.ChooseProxyGroup(originalURL.Hostname(), targetHost)
	var resp *http.Response
	var err error

	if pg != nil {
		maxRetries := pg.MaxRetries
		if maxRetries <= 0 {
			maxRetries = 1
		}
		retryDelay := pg.RetryDelay

		for attempt := 0; attempt <= maxRetries; attempt++ {
			forceTest := attempt > 0
			selectedProxy := lb.SelectProxy(pg, originalReqURL.String(), forceTest)

			clientToUse := client
			if selectedProxy != nil {
				if proxyDialer, dErr := proxy.CreateProxyDialer(*selectedProxy); dErr == nil {
					baseTransport.DialContext = proxyDialer.DialContext
					clientToUse = &http.Client{
						Transport: baseTransport,
						Timeout:   httpCfg.Timeout,
					}
				}
			}

			targetReq, _ := http.NewRequest(r.Method, originalReqURL.String(), bytes.NewReader(reqBodyBytes))
			for name, values := range r.Header {
				if strings.ToLower(name) == "host" {
					continue
				}
				for _, v := range values {
					targetReq.Header.Add(name, v)
				}
			}

			for k, v := range cfg.ServerHeaders {
				if v != "" {
					targetReq.Header.Set(k, v)
				}
			}
			targetReq.Header.Set("Host", targetHost)

			resp, err = dm.doWithRedirect(clientToUse, targetReq, 10, frontendScheme, r.Host, tokenParam)
			if err == nil {
				break
			}
			if attempt == maxRetries {
				http.Error(w, fmt.Sprintf("代理请求失败: %v", err), http.StatusBadGateway)
				return
			}
			time.Sleep(retryDelay)
		}
	} else {
		targetReq, _ := http.NewRequest(r.Method, originalReqURL.String(), bytes.NewReader(reqBodyBytes))
		for name, values := range r.Header {
			if strings.ToLower(name) == "host" {
				continue
			}
			for _, v := range values {
				targetReq.Header.Add(name, v)
			}
		}
		for k, v := range cfg.ServerHeaders {
			if v != "" {
				targetReq.Header.Set(k, v)
			}
		}
		targetReq.Header.Set("Host", targetHost)

		resp, err = dm.doWithRedirect(client, targetReq, 10, frontendScheme, r.Host, tokenParam)
		if err != nil {
			http.Error(w, "无法连接目标服务器: "+err.Error(), http.StatusBadGateway)
			return
		}
	}
	defer resp.Body.Close()

	// ---------- 返回响应 ----------
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.Header().Del("Content-Length")
	w.WriteHeader(resp.StatusCode)

	contentType := resp.Header.Get("Content-Type")
	bufSize := buffer.GetOptimalBufferSize(contentType, originalReqURL.Path)
	isM3U8 := strings.Contains(contentType, "mpegurl")

	if isM3U8 {
		// logger.LogPrintf("DEBUG: 检测到M3U8内容，开始处理...")
		reader := bufio.NewReader(resp.Body)
		seen := make(map[string]struct{})
		for {
			line, err := reader.ReadBytes('\n')
			if len(line) > 0 {
				// 解析行中的URL，记录真实地址映射
				lineStr := string(line)
				if strings.Contains(lineStr, "http://") || strings.Contains(lineStr, "https://") {
					// logger.LogPrintf("DEBUG: 在M3U8中发现URL行: %s", strings.TrimSpace(lineStr))
					// 提取URL并记录映射关系
					re := regexp.MustCompile(`https?://[^/\s]+`)
					matches := re.FindAllString(lineStr, -1)
					for _, match := range matches {
						if parsedURL, err := url.Parse(match); err == nil {
							// logger.LogPrintf("DEBUG: 解析出URL Host: %s", parsedURL.Host)
							// 记录M3U8中出现的地址映射到sourceHost
							// 映射关系: M3U8中的地址 -> source地址 (用于前端显示)
							dm.AddRealHostMapping(parsedURL.Host, sourceHost)
							// logger.LogPrintf("DEBUG: 添加M3U8映射 - ParsedHost: %s -> SourceHost: %s", parsedURL.Host, sourceHost)

							// 同时记录sourceHost到M3U8中地址的映射
							// 用于后端请求时找到真实的target地址
							dm.AddRealHostMapping(sourceHost, parsedURL.Host)
							// logger.LogPrintf("DEBUG: 添加源映射 - SourceHost: %s -> M3U8 Host: %s", sourceHost, parsedURL.Host)
						}
					}
				}

				newLine := dm.replaceSpecialNestedURLClean(string(line), frontendScheme, r.Host, sourceHost, tm, tokenParam, seen)
				if newLine != nil {
					w.Write(newLine)
				}
				updateActive()
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				} else {
					return
				}
			}
		}
	} else {
		buf := buffer.GetBuffer(bufSize)
		defer buffer.PutBuffer(bufSize, buf)
		stream.CopyWithContext(r.Context(), w, resp.Body, buf, bufSize, updateActive, originalReqURL.String())
	}

	logger.LogRequestAndResponse(r, originalReqURL.String(), resp)
}

// ---------------------------
// 处理 301/302 重定向
// ---------------------------

func (dm *DomainMapper) doWithRedirect(client *http.Client, req *http.Request, maxRedirect int, frontendScheme, frontendHost string, tokenParam string) (*http.Response, error) {
	defer dm.CleanTokenManagers()
	reqBodyBytes, _ := io.ReadAll(req.Body)
	// req.Body.Close()

	for i := 0; i < maxRedirect; i++ {
		req.Body = io.NopCloser(bytes.NewReader(reqBodyBytes))
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			loc := resp.Header.Get("Location")
			// resp.Body.Close()
			if loc == "" {
				return resp, nil
			}

			parsedURL, err := url.Parse(loc)
			if err != nil {
				return resp, nil
			}

			// 保持原始重定向URL，后端请求需要使用真实的URL
			finalLoc := loc

			// 检查是否需要进行域名映射替换
			if hostMapped, protocolMapped, found := dm.MapDomain(parsedURL.Host); found {
				newURL := &url.URL{
					Scheme:   chooseScheme(protocolMapped, nil),
					Host:     hostMapped,
					Path:     parsedURL.Path,
					RawQuery: parsedURL.RawQuery,
					Fragment: parsedURL.Fragment,
				}
				finalLoc = newURL.String()
			}

			req, err = http.NewRequest(req.Method, finalLoc, bytes.NewReader(reqBodyBytes))
			if err != nil {
				return nil, err
			}
			for name, values := range req.Header {
				req.Header[name] = values
			}
			continue
		}

		return resp, nil
	}
	return nil, errors.New("too many redirects")
}
