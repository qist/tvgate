package stream

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"github.com/qist/tvgate/auth"
	"github.com/qist/tvgate/logger"
	"net"
	"time"
	// "github.com/qist/tvgate/monitor"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"path/filepath"

	"github.com/qist/tvgate/utils/buffer"
)

// 定义任务结构体用于sync.Pool
type handleTask struct {
	f func()
}

// 创建sync.Pool用于复用任务对象
var handleTaskPool = sync.Pool{
	New: func() interface{} {
		return &handleTask{}
	},
}

// 统一处理响应（重定向、特殊类型、普通内容）
func HandleProxyResponse(ctx context.Context, w http.ResponseWriter, r *http.Request, targetURL string, resp *http.Response, updateActive func()) {
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.LogPrintf("关闭响应体错误: %v", err)
		}
	}()

	logger.LogRequestAndResponse(r, targetURL, resp) // 日志记录

	contentType := resp.Header.Get("Content-Type")

	switch resp.StatusCode {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect:
		handleRedirect(w, r, resp)
		return
	}

	if IsSupportedContentType(contentType) {
		handleSpecialContent(w, r, resp, nil) // 这里不需要buf，因为handleSpecialContent内部会自己获取
		return
	}

	// 检查是否为TS请求，如果是，需要提前清理可能的问题头部
	u, err := url.Parse(targetURL)
	if err != nil {
		logger.LogPrintf("解析目标URL失败: %v", err)
		return
	}
	
	if strings.EqualFold(filepath.Ext(u.Path), ".ts") {
		// 删除可能引起问题的头部，特别是Content-Length
		// 这必须在写入任何响应数据之前完成
		resp.Header.Del("Content-Length")
	}

	// 复制响应头
	CopyHeader(w.Header(), resp.Header, r.ProtoMajor)
	w.WriteHeader(resp.StatusCode)

	// 解析URL并获取最优缓冲区大小
	bufSize := buffer.GetOptimalBufferSize(contentType, u.Path)
	buf := buffer.GetBuffer(bufSize)
	defer buffer.PutBuffer(bufSize, buf)

	// 使用统一的响应复制函数
	copyFunc := func() error {
		return CopyResponse(ctx, w, r, resp, targetURL, buf, bufSize, updateActive, resp.StatusCode)
	}

	// 使用统一的执行函数处理复制操作
	executeCopyWithPool(ctx, r, targetURL, copyFunc, resp)
}

// executeCopyWithPool 使用任务池执行复制操作
func executeCopyWithPool(ctx context.Context, r *http.Request, targetURL string, copyFunc func() error, resp *http.Response) {
	done := make(chan struct{})

	// 从池中获取任务对象
	task := handleTaskPool.Get().(*handleTask)
	task.f = func() {
		defer close(done)
		if err := copyFunc(); err != nil {
			HandleCopyError(r, err, resp)
		}
	}

	// 在goroutine内部执行任务并确保完成后放回池中
	go func() {
		defer func() {
			// 清空任务并放回池中
			task.f = nil
			handleTaskPool.Put(task)
		}()
		task.f()
	}()

	select {
	case <-ctx.Done():
		logger.LogPrintf("客户端断开连接: %s", targetURL)
	case <-done:
		logger.LogPrintf("响应成功完成: %s", targetURL)
	}
}

// Copytext 流式复制 src -> dst，使用 buffer 池，bufio 内部缓存可控
func Copytext(ctx context.Context, dst io.Writer, src io.Reader, buf []byte, updateActive func()) error {
	// 活跃时间心跳
	var activeTicker *time.Ticker
	if updateActive != nil {
		activeTicker = time.NewTicker(5 * time.Second)
		defer activeTicker.Stop()
		defer updateActive() // 退出前更新一次
	}

	// flush 定时器（降低延迟）
	flushTicker := time.NewTicker(200 * time.Millisecond)
	defer flushTicker.Stop()

	// 是否支持 http flush
	flusher, canFlush := dst.(http.Flusher)

	// 统计写入字节数
	bytesWritten := 0

	// 如果 src 是 net.Conn，给它绑 ctx
	if c, ok := src.(net.Conn); ok {
		go func() {
			<-ctx.Done()
			// 打断阻塞 read
			_ = c.SetReadDeadline(time.Now())
		}()
	}

	for {
		// 读取数据
		n, readErr := src.Read(buf)
		if n > 0 {
			// monitor.AddAppInboundBytes(uint64(n))
			written := 0
			for written < n {
				wn, writeErr := dst.Write(buf[written:n])
				if writeErr != nil {
					return fmt.Errorf("写入错误: %w", writeErr)
				}
				written += wn
				bytesWritten += wn
			}
			// monitor.AddAppOutboundBytes(uint64(n))

			// 大数据量触发 flush
			if canFlush && bytesWritten >= 32*1024 {
				flusher.Flush()
				bytesWritten = 0
			}
		}

		// 错误处理
		if readErr != nil {
			if canFlush && bytesWritten > 0 {
				flusher.Flush()
			}
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return fmt.Errorf("读取错误: %w", readErr)
		}

		// 非阻塞检查 ctx / ticker
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-flushTicker.C:
			if canFlush && bytesWritten > 0 {
				flusher.Flush()
				bytesWritten = 0
			}
		case <-activeTicker.C:
			if updateActive != nil {
				updateActive()
			}
		default:
		}
	}
}

func isWebPageContent(contentType, path string) bool {
	ct := strings.ToLower(contentType)
	p := strings.ToLower(path)

	// 去掉参数（; charset=xxx）
	if i := strings.Index(ct, ";"); i != -1 {
		ct = ct[:i]
	}

	// ===== 明确排除流媒体 =====
	if strings.HasPrefix(ct, "video/") ||
		strings.HasPrefix(ct, "audio/") {
		return false
	}

	// ===== image 特判：只允许 svg =====
	if strings.HasPrefix(ct, "image/") {
		return ct == "image/svg+xml"
	}

	// ===== 明确允许的 Web / 文本类型 =====
	switch ct {
	case
		"text/html",
		"text/plain",
		"text/css",
		"text/markdown",

		"application/javascript",
		"application/x-javascript",
		"application/json",
		"application/xml",
		"application/xhtml+xml",
		"application/rss+xml",
		"application/atom+xml",

		"image/svg+xml",
		"application/wasm":
		return true
	}

	// ===== 处理 path（去 query / fragment）=====
	if i := strings.IndexAny(p, "?#"); i != -1 {
		p = p[:i]
	}

	// ===== 扩展名兜底 =====
	switch {
	case strings.HasSuffix(p, ".html"),
		strings.HasSuffix(p, ".htm"),
		strings.HasSuffix(p, ".css"),
		strings.HasSuffix(p, ".js"),
		strings.HasSuffix(p, ".mjs"),
		strings.HasSuffix(p, ".json"),
		strings.HasSuffix(p, ".xml"),
		strings.HasSuffix(p, ".txt"),
		strings.HasSuffix(p, ".md"),
		strings.HasSuffix(p, ".map"),
		strings.HasSuffix(p, ".svg"),
		strings.HasSuffix(p, ".wasm"):
		return true
	}

	return false
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
	//logger.LogPrintf("getTargetURL 处理: 原始路径=%s, 查询参数=%s, 最终URL=%s",
	//	path, r.URL.RawQuery, targetURL)

	return targetURL
}


// CopyWithContext 支持 HTTP hub 模式：按后端URL为键，单上游广播到所有前端
func CopyWithContext(
	ctx context.Context,
	dst http.ResponseWriter,
	src io.Reader,
	buf []byte,
	bufSize int,
	updateActive func(),
	backendKey string,
	statusCode int,
) error {
	h := GetOrCreateHTTPHub(backendKey, statusCode)

	// 检查是否为TS请求，以决定处理方式
	if h.IsTSRequest(backendKey) {
		key := normalizeCacheKey(backendKey)

		// 尝试从流式缓存获取
		if GlobalTSCache != nil {
			if cacheItem, ok := GlobalTSCache.Get(key); ok {
				done := make(chan struct{})
				defer close(done)
				
				// 对于TS缓存读取，删除Content-Length头部以避免长度不匹配问题
				if rw, ok := dst.(http.ResponseWriter); ok {
					rw.Header().Del("Content-Length")
				}
				
				// 从缓存流式读取并写入响应
				if err := cacheItem.ReadAll(dst, done); err != nil {
					// 客户端连接可能已断开，记录但不视为错误
					if err != io.EOF {
						logger.LogPrintf("从缓存读取TS数据时出错: %v", err)
					}
					return nil // 客户端断开连接不是服务器错误
				}
				
				if f, ok := dst.(http.Flusher); ok {
					f.Flush()
				}
				return nil
			}
		}

		// 使用singleflight确保相同URL只进行一次获取操作
		if GlobalTSCache != nil {
			_, err, _ := GlobalTSCache.sf.Do(key, func() (interface{}, error) {
				// 创建新的流式缓存项
				cacheItem, _ := GlobalTSCache.GetOrCreate(key)
				
				// 从源读取并同时写入缓存和响应
				bufRead := make([]byte, 32*1024) // 32KB 分片
				for {
					n, err := src.Read(bufRead)
					if n > 0 {
						// 将数据写入缓存
						chunk := make([]byte, n)
						copy(chunk, bufRead[:n])
						if GlobalTSCache != nil {
							GlobalTSCache.WriteChunkWithByteTracking(cacheItem, chunk)
						}

						// 同时写入响应
						written, wErr := dst.Write(chunk)
						if wErr != nil {
							return nil, wErr
						}
						if written != n {
							return nil, io.ErrShortWrite
						}
						
						if f, ok := dst.(http.Flusher); ok {
							f.Flush()
						}
					}

					if err != nil {
						if err == io.EOF {
							return nil, nil
						}
						return nil, err
					}
				}
			})

			if err != nil {
				return err
			}
			
			return nil
		}

		client := h.AddClient(dst, bufSize)
		defer h.RemoveClient(client)

		// 传递backendKey以区分处理方式
		h.EnsureProducer(ctx, src, buf)

		return client.WriteLoop(ctx, updateActive)
	}

	client := h.AddClient(dst, bufSize)
	defer h.RemoveClient(client)

	// 传递backendKey以区分处理方式
	h.EnsureProducer(ctx, src, buf)

	return client.WriteLoop(ctx, updateActive)
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
func handleSpecialContent(w http.ResponseWriter, r *http.Request, proxyResp *http.Response, buf []byte) {
	// defer proxyResp.Body.Close()

	bufSize := len(buf)
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

// CopyResponse 根据内容类型选择适当的复制方法
func CopyResponse(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	resp *http.Response,
	targetURL string,
	buf []byte,
	bufSize int,
	updateActive func(),
	statusCode int,
) error {
	u, err := url.Parse(targetURL)
	if err != nil {
		return err
	}

	contentType := resp.Header.Get("Content-Type")
	isTS := strings.EqualFold(filepath.Ext(u.Path), ".ts")

	// TS 请求提前清理头
	if isTS {
		w.Header().Del("Content-Length")
	}

	// 🔴 关闭状态：全部退化为 Copytext
	if !isStreamFeatureEnabled() {
		// logger.LogPrintf(
		// 	"stream disabled, fallback to Copytext: %s",
		// 	u.Path,
		// )
		return Copytext(ctx, w, resp.Body, buf, updateActive)
	}

	// 🟢 正常逻辑
	if isWebPageContent(contentType, u.Path) {
		return Copytext(ctx, w, resp.Body, buf, updateActive)
	}

	return CopyWithContext(
		ctx,
		w,
		resp.Body,
		buf,
		bufSize,
		updateActive,
		resp.Request.URL.String(),
		statusCode,
	)
}


func isStreamFeatureEnabled() bool {
	// TSCache 是你流媒体 / FCC / Hub 的“总开关信号”
	return GlobalTSCache != nil
}
