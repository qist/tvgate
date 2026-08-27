package logger

import (
	"net/http"
	"strings"
)

// LogPHPRequest 记录 PHP 模块的访问日志。
// 受全局日志开关控制（LogPrintf 内部判断 enabled）。
// 格式与 LogRequestAndResponse 对齐：
//
//	[remoteAddr] METHOD /php/script.php?id=xxx HTTP/1.1 302 [User-Agent: xxx]
//	[X-Forwarded-For: xxx] [remoteAddr] METHOD /php/script.php?id=xxx HTTP/1.1 302 [User-Agent: xxx]
func LogPHPRequest(r *http.Request, scriptPath string, statusCode int) {
	remoteAddr := r.RemoteAddr
	forwardedFor := r.Header.Get("X-Forwarded-For")
	userAgent := r.Header.Get("User-Agent")

	// 拼接完整 URL（含 query string），对齐 PHP 访问日志习惯
	requestURL := r.URL.RequestURI()
	if !strings.HasPrefix(requestURL, "/") {
		requestURL = "/" + requestURL
	}

	if forwardedFor != "" {
		LogPrintf("[X-Forwarded-For: %s] [%s] %s %s %s %d [User-Agent: %s] [Script: %s]",
			forwardedFor, remoteAddr, r.Method, requestURL, r.Proto, statusCode, userAgent, scriptPath)
	} else {
		LogPrintf("[%s] %s %s %s %d [User-Agent: %s] [Script: %s]",
			remoteAddr, r.Method, requestURL, r.Proto, statusCode, userAgent, scriptPath)
	}
}


