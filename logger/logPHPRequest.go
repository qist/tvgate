package logger

import (
	"fmt"
	"net/http"
	"strings"
)

// LogPHPRequest 记录 PHP 模块的访问日志。
// 受全局日志开关控制（LogPrintf 内部判断 enabled）。
// 格式类似 nginx combined log：
//
//	[remoteAddr] METHOD /php/script.php?id=xxx HTTP/1.1 302 1234 [Host: xxx] [User-Agent: xxx] [Referer: xxx] [Script: xxx]
//	[X-Forwarded-For: xxx] [remoteAddr] METHOD /php/script.php?id=xxx HTTP/1.1 302 1234 [Host: xxx] [User-Agent: xxx] [Referer: xxx] [Script: xxx]
func LogPHPRequest(r *http.Request, scriptPath string, statusCode int, bytesSent int64) {
	remoteAddr := r.RemoteAddr
	forwardedFor := r.Header.Get("X-Forwarded-For")
	userAgent := r.Header.Get("User-Agent")
	referer := r.Header.Get("Referer")
	host := r.Host

	// 拼接完整 URL（含 query string）
	requestURL := r.URL.RequestURI()
	if !strings.HasPrefix(requestURL, "/") {
		requestURL = "/" + requestURL
	}

	// 格式化字节数
	bytesStr := fmt.Sprintf("%d", bytesSent)

	if forwardedFor != "" {
		LogPrintf("[X-Forwarded-For: %s] [%s] %s %s %s %d %s [Host: %s] [User-Agent: %s] [Referer: %s] [Script: %s]",
			forwardedFor, remoteAddr, r.Method, requestURL, r.Proto, statusCode, bytesStr, host, userAgent, referer, scriptPath)
	} else {
		LogPrintf("[%s] %s %s %s %d %s [Host: %s] [User-Agent: %s] [Referer: %s] [Script: %s]",
			remoteAddr, r.Method, requestURL, r.Proto, statusCode, bytesStr, host, userAgent, referer, scriptPath)
	}
}
