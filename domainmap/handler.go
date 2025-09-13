package domainmap

import (
	"bufio"
	"bytes"
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
	"time"

	"github.com/qist/tvgate/auth"
	"github.com/qist/tvgate/config"
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
	}

	// 初始化时清理tokenManagers
	dm.CleanTokenManagers()

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

func (dm *DomainMapper) replaceSpecialNestedURL(parsedURL *url.URL, frontendScheme, frontendHost string, tm *auth.TokenManager) (string, bool, string) {
	originalHost := parsedURL.Host
	innerPath := parsedURL.Path

	// 去掉嵌套的 host 前缀
	if strings.HasPrefix(innerPath, "/"+originalHost) {
		innerPath = strings.TrimPrefix(innerPath, "/"+originalHost)
		if !strings.HasPrefix(innerPath, "/") {
			innerPath = "/" + innerPath
		}
	}

	// 构造新 URL（不改变查询参数）
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
		tokenParam := tm.TokenParamName
		if tokenParam == "" {
			tokenParam = "token"
		}

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
			tokenParam := globalTm.TokenParamName
			if tokenParam == "" {
				tokenParam = "token"
			}

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

// ---------------------------
// M3U8 处理辅助函数
// ---------------------------

func (dm *DomainMapper) replaceSpecialNestedURLClean(
	line string,
	frontendScheme, frontendHost string,
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
			// 清理重复 host
			parts := strings.Split(u.Host+u.Path, "/")
			cleanedParts := []string{}
			last := ""
			for _, p := range parts {
				if p == last && strings.Contains(p, ".") {
					continue
				}
				cleanedParts = append(cleanedParts, p)
				last = p
			}
			u.Path = strings.Join(cleanedParts[1:], "/")
			u.Scheme = frontendScheme
			u.Host = frontendHost

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

	// 如果协议是RTSP，则转发给RTSP处理器
	if protocol == "rtsp" {
		// 构造新的URL路径，格式为 /rtsp/{targetHost}{原始路径}
		newPath := "/rtsp/" + targetHost + r.URL.Path
		cfg := dm.GetDomainConfig(r.Host)
		// RTSP授权验证
		// 1. 首先检查domainmap配置中的授权信息
		tokenParam := cfg.Auth.TokenParamName
		if tokenParam == "" {
			tokenParam = "token"
		}
		
		token := r.URL.Query().Get(tokenParam)
		clientIP := monitor.GetClientIP(r)
		connID := clientIP + "_" + strconv.FormatInt(time.Now().UnixNano(), 10)
		
		// 检查domainmap是否开启了授权
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
				
				// 验证token时使用原始路径而不是RTSP路径
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
					// 验证token时使用原始路径而不是RTSP路径
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
		
		// 创建新的请求
		newReq := r.Clone(r.Context())
		newReq.URL.Path = newPath
		newReq.URL.RawQuery = r.URL.RawQuery
		
		// 转发给下一个处理器（即RTSP处理器）
		RtspToHTTPHandler(w, newReq)
		return
	}

	cfg := dm.GetDomainConfig(r.Host)
	if cfg == nil {
		dm.next.ServeHTTP(w, r)
		return
	}

	frontendScheme := getRequestScheme(r)
	targetURL := &url.URL{
		Scheme:   chooseScheme(protocol, r),
		Host:     targetHost,
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
	}

	reqBodyBytes, _ := io.ReadAll(r.Body)
	r.Body.Close()

	clientIP := monitor.GetClientIP(r)
	hash := md5.Sum([]byte(fmt.Sprintf("%s://%s", targetURL.Scheme, targetURL.Host)))
	connID := clientIP + "_" + hex.EncodeToString(hash[:])

	// 校验 client headers
	if len(cfg.ClientHeaders) > 0 {
		for k, v := range cfg.ClientHeaders {
			if r.Header.Get(k) != v {
				http.Error(w, "Forbidden", http.StatusUnauthorized)
				return
			}
		}
	}

	// token 校验
	tokenParam := cfg.Auth.TokenParamName
	if tokenParam == "" {
		tokenParam = "token"
	}
	// 使用Query().Get()已经会自动URL解码，但为了确保Base64字符不被破坏，我们直接从RawQuery解析
	token := r.URL.Query().Get(tokenParam)
	var tm *auth.TokenManager

	// 获取或创建对应域名映射的TokenManager实例
	if cfg.Auth.TokensEnabled {
		if cfg.Auth.DynamicTokens.EnableDynamic || cfg.Auth.StaticTokens.EnableStatic {
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

			// logger.LogPrintf("token: %v", cfg)
			if !tm.ValidateToken(token, r.URL.Path, connID) {
				http.Error(w, "Forbidden", http.StatusUnauthorized)
				return
			}
		}
	} else {
		// 2. 只有当domainmap没有开启授权时，才检查全局认证
		if globalTm := auth.GetGlobalTokenManager(); globalTm != nil && globalTm.Enabled {
			// 如果全局认证启用且配置了动态或静态token，则进行验证
			if globalTm.DynamicConfig != nil || len(globalTm.StaticTokens) > 0 {
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
	targetReqURL := *targetURL
	if cfg.Auth.TokensEnabled {
		q := targetReqURL.Query()
		q.Del(tokenParam)

		// 直接构建 RawQuery，不使用 Encode()，保持原始格式
		rawQueryParts := []string{}
		for k, vals := range q {
			for _, v := range vals {
				rawQueryParts = append(rawQueryParts, k+"="+v)
			}
		}
		if len(rawQueryParts) > 0 {
			targetReqURL.RawQuery = strings.Join(rawQueryParts, "&")
		} else {
			targetReqURL.RawQuery = ""
		}
	}

	// -------- 使用动态 HTTP 配置创建 Transport 和 Client ----------
	httpCfg := config.Cfg.HTTP
	config.Cfg.SetDefaults()

	dialer := &net.Dialer{
		Timeout:   httpCfg.ConnectTimeout,
		KeepAlive: httpCfg.KeepAlive,
	}

	baseTransport := &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   httpCfg.TLSHandshakeTimeout,
		ResponseHeaderTimeout: httpCfg.ResponseHeaderTimeout,
		ExpectContinueTimeout: httpCfg.ExpectContinueTimeout,
		IdleConnTimeout:       httpCfg.IdleConnTimeout,
		MaxIdleConns:          httpCfg.MaxIdleConns,
		MaxIdleConnsPerHost:   httpCfg.MaxIdleConnsPerHost,
		MaxConnsPerHost:       httpCfg.MaxConnsPerHost,
		DisableKeepAlives:     httpCfg.DisableKeepAlives,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
	}

	client := &http.Client{
		Transport: baseTransport,
		Timeout:   httpCfg.Timeout,
	}

	// ---------- 处理代理或直连 ----------
	pg := rules.ChooseProxyGroup(targetURL.Hostname(), targetHost)
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
			selectedProxy := lb.SelectProxy(pg, targetReqURL.String(), forceTest)

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

			targetReq, _ := http.NewRequest(r.Method, targetReqURL.String(), bytes.NewReader(reqBodyBytes))
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

			resp, err = dm.doWithRedirect(clientToUse, targetReq, 10, frontendScheme, r.Host)
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
		targetReq, _ := http.NewRequest(r.Method, targetReqURL.String(), bytes.NewReader(reqBodyBytes))
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

		resp, err = dm.doWithRedirect(client, targetReq, 10, frontendScheme, r.Host)
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
	bufSize := buffer.GetOptimalBufferSize(contentType, targetReqURL.Path)
	isM3U8 := strings.Contains(contentType, "mpegurl")

	if isM3U8 {
		reader := bufio.NewReader(resp.Body)
		seen := make(map[string]struct{})
		for {
			line, err := reader.ReadBytes('\n')
			if len(line) > 0 {
				newLine := dm.replaceSpecialNestedURLClean(string(line), frontendScheme, r.Host, tm, tokenParam, seen)
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
		stream.CopyWithContext(r.Context(), w, resp.Body, buf, updateActive)
	}

	logger.LogRequestAndResponse(r, targetReqURL.String(), resp)
}

// ---------------------------
// 处理 301/302 重定向
// ---------------------------

func (dm *DomainMapper) doWithRedirect(client *http.Client, req *http.Request, maxRedirect int, frontendScheme, frontendHost string) (*http.Response, error) {
	defer dm.CleanTokenManagers()
	reqBodyBytes, _ := io.ReadAll(req.Body)
	req.Body.Close()

	for i := 0; i < maxRedirect; i++ {
		req.Body = io.NopCloser(bytes.NewReader(reqBodyBytes))
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			loc := resp.Header.Get("Location")
			resp.Body.Close()
			if loc == "" {
				return resp, nil
			}

			parsedURL, err := url.Parse(loc)
			if err != nil {
				return resp, nil
			}

			newLoc, replaced, _ := dm.replaceSpecialNestedURL(parsedURL, frontendScheme, frontendHost, nil)
			if !replaced {
				hostMapped, protocolMapped, found := dm.MapDomain(parsedURL.Host)
				if found {
					newURL := &url.URL{
						Scheme:   chooseScheme(protocolMapped, nil),
						Host:     hostMapped,
						Path:     parsedURL.Path,
						RawQuery: parsedURL.RawQuery,
						Fragment: parsedURL.Fragment,
					}
					newLoc = newURL.String()
				} else {
					newLoc = parsedURL.String()
				}
			}

			req, err = http.NewRequest(req.Method, newLoc, bytes.NewReader(reqBodyBytes))
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
