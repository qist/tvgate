package stream

import (
	"context"
	"errors"
	"fmt"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/utils/buffer"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// 统一处理响应（重定向、特殊类型、普通内容）
func HandleProxyResponse(ctx context.Context, w http.ResponseWriter, r *http.Request, targetURL string, resp *http.Response) {
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.LogPrintf("关闭响应体错误: %v", err)
		}
	}()

	logger.LogRequestAndResponse(r, targetURL, resp) // 日志记录

	switch resp.StatusCode {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect:
		handleRedirect(w, r, resp)
		return
	}

	if isSupportedContentType(resp.Header.Get("Content-Type")) {
		handleSpecialContent(w, r, resp)
		return
	}

	// 复制响应头
	copyHeader(w.Header(), resp.Header, r.ProtoMajor)
	w.WriteHeader(resp.StatusCode)

	u, _ := url.Parse(targetURL)
	contentType := resp.Header.Get("Content-Type")
	bufSize := buffer.GetOptimalBufferSize(contentType, u.Path)

	buf := buffer.GetBuffer(bufSize)
	defer buffer.PutBuffer(bufSize, buf)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := copyWithContext(ctx, w, resp.Body, buf); err != nil {
			handleCopyError(r, err, resp)
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

// handleSpecialContent 处理特殊内容类型的响应（如 m3u8）
func handleSpecialContent(w http.ResponseWriter, r *http.Request, proxyResp *http.Response) {
	defer proxyResp.Body.Close()

	bodyBytes, err := io.ReadAll(proxyResp.Body)
	if err != nil {
		http.Error(w, "读取响应内容失败", http.StatusInternalServerError)
		return
	}

	// 获取代理基础 URL
	scheme := getRequestScheme(r)
	baseURL := fmt.Sprintf("%s://%s", scheme, r.Host)

	lines := strings.Split(string(bodyBytes), "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue // 注释或标签不处理
		}

		// 只处理原始 HTTP/HTTPS URL
		if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
			u, err := url.Parse(line)
			if err != nil {
				continue
			}

			// 已经是代理 URL，跳过
			if u.Host == r.Host {
				continue
			}

			// 添加代理前缀
			lines[i] = fmt.Sprintf("%s/%s", baseURL, line)
		}
	}

	result := strings.Join(lines, "\n")

	// 复制响应头并移除 Content-Length
	copyHeader(w.Header(), proxyResp.Header, r.ProtoMajor)
	w.Header().Del("Content-Length")
	w.WriteHeader(proxyResp.StatusCode)
	w.Write([]byte(result))
}

// 修改 handleRedirect 函数
func handleRedirect(w http.ResponseWriter, r *http.Request, proxyResp *http.Response) {
	location := proxyResp.Header.Get("Location")
	if location == "" {
		w.WriteHeader(proxyResp.StatusCode)
		return
	}

	logger.LogPrintf("处理重定向: %s", location)

	// 构建新的重定向URL
	scheme := getRequestScheme(r)
	newLocation := fmt.Sprintf("%s://%s/%s",
		scheme,
		r.Host,
		strings.TrimLeft(location, "/"))

	logger.LogPrintf("重定向到: %s", newLocation)
	w.Header().Set("Location", newLocation)
	w.WriteHeader(proxyResp.StatusCode)
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
func isSupportedContentType(contentType string) bool {
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

func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader, buf []byte) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			n, readErr := src.Read(buf)
			if n > 0 {
				written := 0
				for written < n {
					wn, writeErr := dst.Write(buf[written:n])
					if writeErr != nil {
						return fmt.Errorf("写入错误: %w", writeErr)
					}
					written += wn
				}

				// 强制刷新（流式传输时推荐）
				if f, ok := dst.(http.Flusher); ok {
					f.Flush()
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

func handleCopyError(r *http.Request, err error, proxyResp *http.Response) {
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
func copyHeader(dst, src http.Header, protoMajor int) {
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
