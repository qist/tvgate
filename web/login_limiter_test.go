package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAuthLimiterLockAndReset(t *testing.T) {
	l := newAuthLimiter()
	now := time.Now()
	key := "login:1.2.3.4"

	for i := 0; i < authMaxFailures-1; i++ {
		if locked, _ := l.Fail(key, now); locked {
			t.Fatalf("第 %d 次失败不应锁定", i+1)
		}
		if ok, _ := l.Allow(key, now); !ok {
			t.Fatalf("未达阈值前应允许尝试（第 %d 次）", i+1)
		}
	}

	locked, wait := l.Fail(key, now)
	if !locked || wait != authLockDuration {
		t.Fatalf("第 %d 次失败应锁定 %s，实际 locked=%v wait=%s", authMaxFailures, authLockDuration, locked, wait)
	}
	if ok, remain := l.Allow(key, now); ok || remain <= 0 {
		t.Fatalf("锁定期间应拒绝，实际 ok=%v remain=%s", ok, remain)
	}

	// 锁定到期后自动恢复
	later := now.Add(authLockDuration + time.Second)
	if ok, _ := l.Allow(key, later); !ok {
		t.Fatal("锁定到期后应允许尝试")
	}

	// 成功后清零
	l.Fail(key, later)
	l.Reset(key)
	for i := 0; i < authMaxFailures-1; i++ {
		if locked, _ := l.Fail(key, later); locked {
			t.Fatal("Reset 后应重新计数")
		}
	}
}

func TestAuthLimiterIndependentKeys(t *testing.T) {
	l := newAuthLimiter()
	now := time.Now()
	for i := 0; i < authMaxFailures; i++ {
		l.Fail("login:1.1.1.1", now)
	}
	if ok, _ := l.Allow("login:1.1.1.1", now); ok {
		t.Fatal("1.1.1.1 应被锁定")
	}
	if ok, _ := l.Allow("login:2.2.2.2", now); !ok {
		t.Fatal("其他 IP 不应受影响")
	}
	if ok, _ := l.Allow("elevate:1.1.1.1", now); !ok {
		t.Fatal("不同 scope 不应互相影响")
	}
}

func TestClientIPForAuthLimit(t *testing.T) {
	// 同机反代（回环对端）才信任代理头
	r1 := httptest.NewRequest(http.MethodPost, "/web/login", nil)
	r1.RemoteAddr = "127.0.0.1:54321"
	r1.Header.Set("X-Forwarded-For", "203.0.113.5, 10.0.0.1")
	if got := clientIPForAuthLimit(r1); got != "203.0.113.5" {
		t.Fatalf("回环对端应取 XFF 首个 IP，got %q", got)
	}

	r2 := httptest.NewRequest(http.MethodPost, "/web/login", nil)
	r2.RemoteAddr = "127.0.0.1:54321"
	r2.Header.Set("X-Real-IP", "203.0.113.9")
	if got := clientIPForAuthLimit(r2); got != "203.0.113.9" {
		t.Fatalf("回环对端应优先取 X-Real-IP，got %q", got)
	}

	// 非回环对端忽略伪造头
	r3 := httptest.NewRequest(http.MethodPost, "/web/login", nil)
	r3.RemoteAddr = "198.51.100.7:1234"
	r3.Header.Set("X-Forwarded-For", "203.0.113.5")
	if got := clientIPForAuthLimit(r3); got != "198.51.100.7" {
		t.Fatalf("非回环对端应忽略 XFF，got %q", got)
	}
}

func TestAuthLimiterHTTP429(t *testing.T) {
	h := NewConfigHandler(WebConfig{Enabled: true, Username: "admin", Password: "admin", Path: "/web/"})

	req := httptest.NewRequest(http.MethodPost, "/web/login", nil)
	req.RemoteAddr = "203.0.113.77:4567" // 独立 IP，避免与其他用例互相影响
	for i := 0; i < authMaxFailures; i++ {
		h.recordAuthFailure(req, "login", "admin")
	}

	rec := httptest.NewRecorder()
	if h.acquireAuthAttempt(rec, req, "login") {
		t.Fatal("达到阈值后应返回 429")
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("状态码 = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("缺少 Retry-After 头")
	}

	// 其他 IP 不受影响
	req2 := httptest.NewRequest(http.MethodPost, "/web/login", nil)
	req2.RemoteAddr = "203.0.113.78:4567"
	if !h.acquireAuthAttempt(httptest.NewRecorder(), req2, "login") {
		t.Fatal("其他 IP 不应被锁定")
	}
}
