package web

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qist/tvgate/logger"
)

// 登录 / 二次授权失败限流：同一客户端 IP 在时间窗口内连续失败达到阈值后临时锁定，
// 缓解默认/弱口令被在线暴力破解（面板直接暴露公网时尤其重要）。
//
// 说明：
//   - 计数保存在内存，进程重启后清零；足够挡住自动化爆破，不替代强口令与网络访问控制；
//   - 按 IP 计数而非按用户名：避免攻击者故意失败把管理员账号锁死（账号型 DoS）；
//   - 仅当直连对端是回环地址时才信任 X-Real-IP / X-Forwarded-For（同机 Nginx 反代场景），
//     否则伪造请求头即可绕过限流或陷害其他 IP。

const (
	authMaxFailures  = 5                // 窗口内允许的失败次数
	authFailWindow   = 5 * time.Minute  // 失败计数窗口
	authLockDuration = 5 * time.Minute  // 达到阈值后的锁定时长
	authGCCadence    = 10 * time.Minute // 清理过期记录的最小间隔
)

type authAttempt struct {
	failures    int
	windowStart time.Time
	lockedUntil time.Time
	lastSeen    time.Time
}

type authLimiter struct {
	mu      sync.Mutex
	entries map[string]*authAttempt
	lastGC  time.Time
}

var webAuthLimiter = newAuthLimiter()

func newAuthLimiter() *authLimiter {
	return &authLimiter{entries: make(map[string]*authAttempt), lastGC: time.Now()}
}

// Allow 判断当前 key 是否允许尝试；不允许时返回剩余锁定时间。
func (l *authLimiter) Allow(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.gcLocked(now)

	a := l.entries[key]
	if a == nil {
		return true, 0
	}
	if now.Before(a.lockedUntil) {
		return false, a.lockedUntil.Sub(now)
	}
	// 窗口已过期：重新计数
	if now.Sub(a.windowStart) > authFailWindow {
		delete(l.entries, key)
	}
	return true, 0
}

// Fail 记录一次失败；达到阈值时锁定，并返回锁定时长。
func (l *authLimiter) Fail(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	a := l.entries[key]
	if a == nil || now.Sub(a.windowStart) > authFailWindow {
		a = &authAttempt{windowStart: now}
		l.entries[key] = a
	}
	a.failures++
	a.lastSeen = now
	if a.failures >= authMaxFailures {
		a.lockedUntil = now.Add(authLockDuration)
		a.failures = 0
		a.windowStart = now
		return true, authLockDuration
	}
	return false, 0
}

// Reset 认证成功后清除该 key 的失败记录。
func (l *authLimiter) Reset(key string) {
	l.mu.Lock()
	delete(l.entries, key)
	l.mu.Unlock()
}

// gcLocked 清理长时间无活动的记录（调用方需持有锁）。
func (l *authLimiter) gcLocked(now time.Time) {
	if now.Sub(l.lastGC) < authGCCadence {
		return
	}
	l.lastGC = now
	for k, a := range l.entries {
		if now.After(a.lockedUntil) && now.Sub(a.lastSeen) > 2*authFailWindow {
			delete(l.entries, k)
		}
	}
}

// clientIPForAuthLimit 返回用于限流的客户端 IP。
func clientIPForAuthLimit(r *http.Request) string {
	peer := r.RemoteAddr
	if host, _, err := net.SplitHostPort(peer); err == nil {
		peer = host
	}
	peer = strings.TrimSpace(peer)

	// 只有同机反代（127.0.0.1 / ::1）才信任代理头
	if ip := net.ParseIP(peer); ip != nil && ip.IsLoopback() {
		if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" && net.ParseIP(xr) != nil {
			return xr
		}
		if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
			first := strings.TrimSpace(strings.Split(xff, ",")[0])
			if net.ParseIP(first) != nil {
				return first
			}
		}
	}
	return peer
}

// acquireAuthAttempt 在密码校验前调用：被锁定则写 429 并返回 false。
func (h *ConfigHandler) acquireAuthAttempt(w http.ResponseWriter, r *http.Request, scope string) bool {
	key := scope + ":" + clientIPForAuthLimit(r)
	ok, wait := webAuthLimiter.Allow(key, time.Now())
	if ok {
		return true
	}
	secs := int(wait.Seconds()) + 1
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"code":        429,
		"retry_after": secs,
		"msg":         fmt.Sprintf("失败次数过多，已临时锁定，请 %d 秒后再试", secs),
	})
	return false
}

// recordAuthFailure 记录一次密码失败；达到阈值时写审计日志。
func (h *ConfigHandler) recordAuthFailure(r *http.Request, scope, username string) {
	ip := clientIPForAuthLimit(r)
	if locked, wait := webAuthLimiter.Fail(scope+":"+ip, time.Now()); locked {
		logger.LogPrintf("🔒 [web] %s 连续失败 %d 次，已锁定 %s（IP %s，用户 %q）",
			scope, authMaxFailures, wait, ip, username)
	}
}

// resetAuthFailures 认证成功后清除失败计数。
func (h *ConfigHandler) resetAuthFailures(r *http.Request, scope string) {
	webAuthLimiter.Reset(scope + ":" + clientIPForAuthLimit(r))
}
