package stream

import (
	"bufio"
	"context"
	"errors"
	"fmt"

	"github.com/qist/tvgate/auth"
	"github.com/qist/tvgate/logger"

	// "github.com/qist/tvgate/monitor"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/qist/tvgate/utils/buffer"
)

// 统一处理响应（重定向、特殊类型、普通内容）
func HandleProxyResponse(ctx context.Context, w http.ResponseWriter, r *http.Request, targetURL string, resp *http.Response, updateActive func()) {
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.LogPrintf("关闭响应体错误: %v", err)
		}
	}()

	logger.LogRequestAndResponse(r, targetURL, resp) // 日志记录
	u, _ := url.Parse(targetURL)
	contentType := resp.Header.Get("Content-Type")
	bufSize := buffer.GetOptimalBufferSize(contentType, u.Path)

	buf := NewStreamRingBuffer(bufSize)
	switch resp.StatusCode {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect:
		handleRedirect(w, r, resp)
		return
	}

	if IsSupportedContentType(resp.Header.Get("Content-Type")) {
		handleSpecialContent(w, r, resp, buf)
		return
	}

	// 复制响应头
	CopyHeader(w.Header(), resp.Header, r.ProtoMajor)
	w.WriteHeader(resp.StatusCode)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := CopyWithContext(ctx, w, resp.Body, buf, updateActive); err != nil {
			HandleCopyError(r, err, resp)
		}
	}()

	select {
	case <-ctx.Done():
		logger.LogPrintf("客户端断开连接: %s", targetURL)
	case <-done:
		logger.LogPrintf("响应成功完成: %s", targetURL)
	}
}

// getRequestScheme 返回客户端真实使用的协议 (http/https)
func getRequestScheme(r *http.Request) string {
	// 1. 先看标准头
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		return proto
	}

	// 2. Cloudflare 专用头
	if cfVisitor := r.Header.Get("CF-Visitor"); strings.Contains(cfVisitor, "https") {
		return "https"
	}

	// 3. 标准 Forwarded 头
	if forwarded := r.Header.Get("Forwarded"); strings.Contains(forwarded, "proto=https") {
		return "https"
	}

	// 4. 没有代理头就看后端是否启用 TLS
	if r.TLS != nil {
		return "https"
	}

	return "http"
}

// 支持的内容类型列表
var supportedContentTypes = []string{
	"application/vnd.apple.mpegurl",
	"application/x-mpegurl",
	"audio/mpegurl",
	"audio/x-mpegurl",
	"application/mpegurl",
}

// 检查内容类型是否受支持
func IsSupportedContentType(contentType string) bool {
	contentType = strings.ToLower(contentType)
	for _, t := range supportedContentTypes {
		if strings.HasPrefix(contentType, strings.ToLower(t)) {
			return true
		}
	}
	return false
}

// getTargetPath 获取目标路径
func GetTargetPath(r *http.Request) string {
	return r.URL.Path[1:] // 去除开头的 /
}

// getTargetURL 获取完整的目标URL
func GetTargetURL(r *http.Request, targetPath string) string {
	// 分离路径和查询参数
	pathAndQuery := strings.SplitN(targetPath, "?", 2)
	path := pathAndQuery[0]
	var query string
	if len(pathAndQuery) > 1 {
		query = "?" + pathAndQuery[1]
	}

	// 处理URL路径部分
	if strings.HasPrefix(path, "http:/") && !strings.HasPrefix(path, "http://") {
		path = "http://" + strings.TrimPrefix(path, "http:/")
	} else if strings.HasPrefix(path, "https:/") && !strings.HasPrefix(path, "https://") {
		path = "https://" + strings.TrimPrefix(path, "https:/")
	} else if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		path = "http://" + path
	}

	// 重新组合路径和查询参数
	targetURL := path + query

	// 保留原始请求中的查询参数
	if r.URL.RawQuery != "" {
		if strings.Contains(targetURL, "?") {
			targetURL += "&" + r.URL.RawQuery
		} else {
			targetURL += "?" + r.URL.RawQuery
		}
	}
	logger.LogPrintf("getTargetURL 处理: 原始路径=%s, 查询参数=%s, 最终URL=%s",
		path, r.URL.RawQuery, targetURL)

	return targetURL
}

// CopyWithContext 流式复制 src -> dst，使用 buffer 池，bufio 内部缓存可控
func CopyWithContext(ctx context.Context, dst io.Writer, src io.Reader, buf *streamRingBuffer, updateActive func()) error {
	tmp := make([]byte, buf.cap)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			n, readErr := src.Read(tmp)
			if n > 0 {
				buf.Push(tmp[:n])
				out := buf.Pop()
				if out != nil {
					written := 0
					for written < len(out) {
						wn, writeErr := dst.Write(out[written:])
						if writeErr != nil {
							return fmt.Errorf("写入错误: %w", writeErr)
						}
						written += wn
					}
					if f, ok := dst.(http.Flusher); ok {
						f.Flush()
					}
					if updateActive != nil {
						updateActive()
					}
				}
			}
			if readErr != nil {
				if errors.Is(readErr, io.EOF) {
					return nil
				}
				return fmt.Errorf("读取错误: %w", readErr)
			}
		}
	}
}

func HandleCopyError(r *http.Request, err error, proxyResp *http.Response) {
	if errors.Is(err, context.Canceled) {
		logger.LogPrintf("传输被取消: %v, URL: %s", err, proxyResp.Request.URL.String())
	} else if errors.Is(err, context.DeadlineExceeded) {
		logger.LogPrintf("传输超时: %v, URL: %s", err, proxyResp.Request.URL.String())
	} else {
		logger.LogPrintf("[%s] 前端关闭连接 %s", r.RemoteAddr, proxyResp.Request.URL.String())
	}

	// 可选：向客户端返回错误
	// if w.Header().Get("Content-Length") == "" {
	// 	http.Error(w, "服务器错误", http.StatusInternalServerError)
	// }
}

// copyHeadersExceptSensitive 复制 HTTP 头部，跳过敏感或特殊头部
func CopyHeadersExceptSensitive(dst http.Header, src http.Header, protoMajor int) {
	for k, vv := range src {
		lowerKey := strings.ToLower(k)
		// 跳过不兼容头
		switch lowerKey {
		case "connection", "proxy-connection", "keep-alive",
			"transfer-encoding", "upgrade", "te", "trailer", "trailers":
			continue
		}
		// 如果是 HTTP/2，不允许 keep-alive 之类
		if protoMajor == 2 && (lowerKey == "keep-alive" || lowerKey == "connection") {
			continue
		}
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

// copyHeader 复制 HTTP 头部，按协议版本过滤不兼容字段
// - dst: 目标 Header（客户端或后端）
// - src: 来源 Header（后端响应或客户端请求）
// - protoMajor: 客户端使用的协议版本，1 = HTTP/1.x, 2 = HTTP/2
func CopyHeader(dst, src http.Header, protoMajor int) {
	for k, vv := range src {
		lowerKey := strings.ToLower(k)

		// 永远不应该透传的头
		switch lowerKey {
		case "host", "proxy-authorization", "proxy-connection":
			continue
		}

		// HTTP/2 不允许的头
		if protoMajor == 2 {
			switch lowerKey {
			case "connection", "keep-alive", "transfer-encoding",
				"upgrade", "te", "trailer", "trailers":
				continue
			}
		}

		// 复制剩余头
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

// handleRedirect 处理 HTTP 301/302 重定向
func handleRedirect(w http.ResponseWriter, r *http.Request, proxyResp *http.Response) {
	location := proxyResp.Header.Get("Location")
	if location == "" {
		w.WriteHeader(proxyResp.StatusCode)
		return
	}

	logger.LogPrintf("处理重定向: %s", location)

	scheme := getRequestScheme(r)
	tm := auth.GetGlobalTokenManager()
	tokenParam := "token"
	if tm != nil && tm.TokenParamName != "" {
		tokenParam = tm.TokenParamName
	}

	newLocation := location

	if strings.HasPrefix(location, "http://") || strings.HasPrefix(location, "https://") {
		baseURL := fmt.Sprintf("%s://%s", scheme, r.Host)
		newLocation = joinBaseWithFullURL(baseURL, location)
	} else {
		// 相对路径
		newLocation = fmt.Sprintf("%s://%s/%s", scheme, r.Host, strings.TrimLeft(location, "/"))
	}

	// 添加 token 明文
	if tm != nil && tm.Enabled {
		if !strings.Contains(newLocation, tokenParam+"=") {
			token := generateToken(tm, location)
			if token != "" {
				if strings.Contains(newLocation, "?") {
					newLocation += "&" + tokenParam + "=" + token
				} else {
					newLocation += "?" + tokenParam + "=" + token
				}
			}
		}
	}

	logger.LogPrintf("重定向到: %s", newLocation)
	w.Header().Set("Location", newLocation)
	w.WriteHeader(proxyResp.StatusCode)
}

// handleSpecialContent 处理 m3u8 文件内容
func handleSpecialContent(w http.ResponseWriter, r *http.Request, proxyResp *http.Response, buf *streamRingBuffer) {
	defer proxyResp.Body.Close()

	bufSize := buf.cap
	reader := bufio.NewReaderSize(proxyResp.Body, bufSize)

	scheme := getRequestScheme(r)
	baseURL := fmt.Sprintf("%s://%s", scheme, r.Host)

	tm := auth.GetGlobalTokenManager()
	tokenParam := "token"
	if tm != nil && tm.TokenParamName != "" {
		tokenParam = tm.TokenParamName
	}

	seen := make(map[string]struct{})
	var resultLines []string

	for {
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "读取响应内容失败", http.StatusInternalServerError)
			return
		}

		line = strings.TrimSpace(line)
		if line == "" {
			if err == io.EOF {
				break
			}
			continue
		}

		// --- 处理 EXT 标签 ---
		if strings.HasPrefix(line, "#") {
			if strings.Contains(line, "URI=\"") {
				re := regexp.MustCompile(`URI="([^"]+)"`)
				line = re.ReplaceAllStringFunc(line, func(match string) string {
					uri := re.FindStringSubmatch(match)[1]
					newURI := uri
					token := ""
					if tm != nil && tm.Enabled {
						token = generateToken(tm, uri)
					}
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
			resultLines = append(resultLines, line)
			if err == io.EOF {
				break
			}
			continue
		}

		// --- 普通行 (TS/URL) ---
		newLine := line
		token := ""
		if tm != nil && tm.Enabled {
			token = generateToken(tm, line)
		}
		if token != "" {
			if strings.Contains(newLine, "?") {
				newLine += "&" + tokenParam + "=" + token
			} else {
				newLine += "?" + tokenParam + "=" + token
			}
		}

		// 去重
		if _, exists := seen[newLine]; exists {
			if err == io.EOF {
				break
			}
			continue
		}
		seen[newLine] = struct{}{}

		// ✅ 只有 http/https 时才拼接 baseURL
		if strings.HasPrefix(newLine, "http://") || strings.HasPrefix(newLine, "https://") {
			newLine = joinBaseWithFullURL(baseURL, newLine)
		}

		resultLines = append(resultLines, newLine)

		if err == io.EOF {
			break
		}
	}

	// 拼接结果
	result := strings.Join(resultLines, "\n") + "\n"

	CopyHeader(w.Header(), proxyResp.Header, r.ProtoMajor)
	w.Header().Del("Content-Length")
	w.WriteHeader(proxyResp.StatusCode)
	_, _ = w.Write([]byte(result))
}

// joinBaseWithFullURL 拼接 baseURL 和完整 URL，不做任何 encode
func joinBaseWithFullURL(baseURL, fullURL string) string {
	baseURL = strings.TrimRight(baseURL, "/")
	fullURL = strings.TrimLeft(fullURL, "/")
	return baseURL + "/" + fullURL
}

// generateToken 明文生成 token（动态或静态）
func generateToken(tm *auth.TokenManager, path string) string {
	if tm == nil || !tm.Enabled {
		return ""
	}
	if tm.DynamicTokens != nil {
		if tok, err := tm.GenerateDynamicToken(path); err == nil && tok != "" {
			return tok
		}
	}
	if len(tm.StaticTokens) > 0 {
		for st := range tm.StaticTokens {
			return st
		}
	}
	return ""
}
