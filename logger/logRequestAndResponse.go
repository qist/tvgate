package logger
import (
	"net/http"
)
// 添加日志函数
func LogRequestAndResponse(r *http.Request, targetURL string, proxyResp *http.Response) {
	remoteAddr := r.RemoteAddr
	forwardedFor := r.Header.Get("X-Forwarded-For")
	userAgent := r.Header.Get("User-Agent")

	if forwardedFor != "" {
		LogPrintf("[X-Forwarded-For: %s] [%s] %s %s %s %d [User-Agent: %s]",
			forwardedFor, remoteAddr, r.Method, targetURL, r.Proto, proxyResp.StatusCode, userAgent)
	} else {
		LogPrintf("[%s] %s %s %s %d [User-Agent: %s]",
			remoteAddr, r.Method, targetURL, r.Proto, proxyResp.StatusCode, userAgent)
	}
}