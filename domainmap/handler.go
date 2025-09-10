package domainmap

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"errors"
	// "context"
	"fmt"
	"io"
	// "net"
	"crypto/tls"
	"net/http"
	"net/url"
	// "regexp"
	"strings"
	"time"

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
	mappings        DomainMapList
	client          *http.Client
	next            http.Handler
	redirectHandler *RedirectHandler
}

type RedirectHandler struct {
	domainMapper *DomainMapper
	maxRedirects int
}

// ---------------------------
// 初始化
// ---------------------------

func NewDomainMapper(mappings DomainMapList, client *http.Client, next http.Handler) *DomainMapper {
	if client == nil {
		client = &http.Client{
			Timeout: 30 * time.Second,
		}
	}

	dm := &DomainMapper{
		mappings: mappings,
		client:   client,
		next:     next,
	}

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
	for _, mapping := range dm.mappings {
		if mapping.Source == host {
			return mapping.Target, mapping.Protocol, true
		}
	}
	if idx := strings.Index(host, ":"); idx != -1 {
		hostWithoutPort := host[:idx]
		for _, mapping := range dm.mappings {
			if mapping.Source == hostWithoutPort {
				return mapping.Target, mapping.Protocol, true
			}
		}
	}
	return "", "", false
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

func (dm *DomainMapper) replaceSpecialNestedURL(parsedURL *url.URL, frontendScheme, frontendHost string) (string, bool) {
	originalHost := parsedURL.Host
	innerPath := parsedURL.Path

	// 去掉重复 Host 
	if strings.HasPrefix(innerPath, "/"+originalHost) {
		innerPath = strings.TrimPrefix(innerPath, "/"+originalHost)
		if !strings.HasPrefix(innerPath, "/") {
			innerPath = "/" + innerPath
		}
	}

	// 构建新的 URL
	newURL := &url.URL{
		Scheme:   frontendScheme,
		Host:     frontendHost,
		Path:     innerPath,
		RawQuery: parsedURL.RawQuery,
		Fragment: parsedURL.Fragment,
	}

	// 如果替换后 URL 与原始相同，说明没有修改
	if newURL.String() == parsedURL.String() {
		return parsedURL.String(), false
	}
	return newURL.String(), true
}

// ---------------------------
// HTTP 转发（完全流式）
// ---------------------------

func (dm *DomainMapper) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	targetHost, protocol, found := dm.MapDomain(r.Host)
	if !found {
		dm.next.ServeHTTP(w, r)
		return
	}

	// logger.LogPrintf("DEBUG", "域名映射: %s -> %s (%s)", r.Host, targetHost, protocol)
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
	raw := fmt.Sprintf("%s://%s", targetURL.Scheme, targetURL.Host)
	h := md5.Sum([]byte(raw))
	hashStr := hex.EncodeToString(h[:])
	connID := clientIP + "_" + hashStr

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

	var resp *http.Response
	var err error

	// 选择代理组
	hostname := targetURL.Hostname()
	pg := rules.ChooseProxyGroup(hostname, targetHost)

	if pg != nil {
		maxRetries := pg.MaxRetries
		if maxRetries <= 0 {
			maxRetries = 1
		}
		retryDelay := pg.RetryDelay

		for attempt := 0; attempt <= maxRetries; attempt++ {
			forceTest := attempt > 0
			selectedProxy := lb.SelectProxy(pg, targetURL.String(), forceTest)

			var clientToUse *http.Client
			if selectedProxy != nil {
				dialer, dErr := proxy.CreateProxyDialer(*selectedProxy)
				if dErr == nil {
					transport := &http.Transport{
						DialContext:           dialer.DialContext,
						TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
						ResponseHeaderTimeout: 10 * time.Second,
						IdleConnTimeout:       5 * time.Second,
						TLSHandshakeTimeout:   10 * time.Second,
						ExpectContinueTimeout: 1 * time.Second,
						MaxIdleConns:          100,
						MaxIdleConnsPerHost:   4,
						MaxConnsPerHost:       8,
					}
					clientToUse = &http.Client{
						Timeout:   30 * time.Second,
						Transport: transport,
					}
				}
			}
			if clientToUse == nil {
				clientToUse = dm.client
			}

			targetReq, _ := http.NewRequest(r.Method, targetURL.String(), bytes.NewReader(reqBodyBytes))
			for name, values := range r.Header {
				if strings.ToLower(name) == "host" {
					continue
				}
				for _, v := range values {
					targetReq.Header.Add(name, v)
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
		targetReq, _ := http.NewRequest(r.Method, targetURL.String(), bytes.NewReader(reqBodyBytes))
		for name, values := range r.Header {
			if strings.ToLower(name) == "host" {
				continue
			}
			for _, v := range values {
				targetReq.Header.Add(name, v)
			}
		}
		targetReq.Header.Set("Host", targetHost)

		resp, err = dm.doWithRedirect(dm.client, targetReq, 10, frontendScheme, r.Host)
		if err != nil {
			http.Error(w, "无法连接目标服务器: "+err.Error(), http.StatusBadGateway)
			return
		}
	}
	defer resp.Body.Close()

	// 复制响应头
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.Header().Del("Content-Length")
	w.WriteHeader(resp.StatusCode)
	contentType := resp.Header.Get("Content-Type")
	bufSize := buffer.GetOptimalBufferSize(contentType, targetURL.Path)
	isM3U8 := strings.Contains(resp.Header.Get("Content-Type"), "mpegurl")
	if isM3U8 {
		reader := bufio.NewReader(resp.Body)
		seen := make(map[string]struct{}) // 去重
		for {
			line, err := reader.ReadBytes('\n')
			if len(line) > 0 {
				trimmed := strings.TrimSpace(string(line))
				if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
					parsedURL, parseErr := url.Parse(trimmed)
					if parseErr == nil {
						newLine := dm.replaceSpecialNestedURLClean(parsedURL, frontendScheme, r.Host, seen)
						if len(newLine) > 0 {
							line = append(newLine, '\n')
						} else {
							continue
						}
					}
				}
				if _, wErr := w.Write(line); wErr != nil {
					return
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
		stream.CopyWithContext(r.Context(), w, resp.Body, buf, updateActive) // 出站流量统计
	}

	logger.LogRequestAndResponse(r, targetURL.String(), resp)
}

// 新增辅助函数：清理重复 Host，并去重
func (dm *DomainMapper) replaceSpecialNestedURLClean(parsedURL *url.URL, frontendScheme, frontendHost string, seen map[string]struct{}) []byte {
	newStr, replaced := dm.replaceSpecialNestedURL(parsedURL, frontendScheme, frontendHost)
	if !replaced {
		hostMapped, protocolMapped, found := dm.MapDomain(parsedURL.Host)
		if found {
			u := &url.URL{
				Scheme:   chooseScheme(protocolMapped, nil),
				Host:     hostMapped,
				Path:     parsedURL.Path,
				RawQuery: parsedURL.RawQuery,
				Fragment: parsedURL.Fragment,
			}
			newStr = u.String()
		} else {
			newStr = parsedURL.String()
		}
	}

	// 清理重复 host
	parts := strings.Split(newStr, "/")
	cleanedParts := []string{}
	last := ""
	for _, p := range parts {
		if p == last && strings.Contains(p, ".") {
			continue
		}
		cleanedParts = append(cleanedParts, p)
		last = p
	}
	newStr = strings.Join(cleanedParts, "/")

	if _, exists := seen[newStr]; exists {
		return nil
	}
	seen[newStr] = struct{}{}
	return []byte(newStr)
}

// doWithRedirect 处理 301/302 重定向，最多 maxRedirect 次
func (dm *DomainMapper) doWithRedirect(client *http.Client, req *http.Request, maxRedirect int, frontendScheme, frontendHost string) (*http.Response, error) {
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

			// 统一替换 URL
			newLoc, replaced := dm.replaceSpecialNestedURL(parsedURL, frontendScheme, frontendHost)
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

			// logger.LogPrintf("DEBUG", "Redirect replace: %s -> %s", loc, newLoc)
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
