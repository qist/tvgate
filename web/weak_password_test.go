package web

import (
	"net/http/httptest"
	"testing"

	"github.com/qist/tvgate/config"
)

func TestIsWeakWebPassword(t *testing.T) {
	cases := []struct {
		user, pass string
		weak       bool
	}{
		{"admin", "admin", true},    // 内嵌默认口令
		{"admin", "Admin", true},    // 大小写变体
		{"bob", "bob", true},        // 与用户名相同
		{"admin", "123456", true},   // 常见弱口令 + 过短
		{"admin", "admin123", true}, // 常见弱口令
		{"admin", "short", true},    // 不足 8 位
		{"admin", "", false},        // 空口令：登录被拒，不按弱口令处理
		{"admin", "MyS3cret!pass", false},
		{"ops", "0p3nS3sam3-2026", false},
	}
	for _, c := range cases {
		weak, hint := isWeakWebPassword(c.user, c.pass)
		if weak != c.weak {
			t.Errorf("isWeakWebPassword(%q,%q) = %v(%s), want %v", c.user, c.pass, weak, hint, c.weak)
		}
		if weak && hint == "" {
			t.Errorf("isWeakWebPassword(%q,%q) 缺少原因说明", c.user, c.pass)
		}
	}
}

func TestCheckWeakPasswordGuard(t *testing.T) {
	oldWeb := config.Cfg.Web
	t.Cleanup(func() {
		config.CfgMu.Lock()
		config.Cfg.Web = oldWeb
		config.CfgMu.Unlock()
	})

	config.CfgMu.Lock()
	config.Cfg.Web.Enabled = true
	config.Cfg.Web.Username = "admin"
	config.Cfg.Web.Password = "admin"
	config.Cfg.Web.Path = "/web/"
	config.CfgMu.Unlock()

	h := NewConfigHandler(WebConfig{Enabled: true, Username: "admin", Password: "admin", Path: "/web/"})

	// 弱口令 + 高危操作 → 拦截
	for _, path := range []string{"/web/config/save-php", "/web/config/save", "/web/api/tasks/config/save", "/web/api/code/save"} {
		rec := httptest.NewRecorder()
		if h.checkWeakPasswordGuard(rec, httptest.NewRequest("POST", path, nil)) {
			t.Errorf("%s 应被弱口令闸门拦截", path)
		}
		if rec.Code != 403 {
			t.Errorf("%s 状态码 = %d, want 403", path, rec.Code)
		}
	}

	// 弱口令 + 改密码 / 读取配置 → 放行
	for _, path := range []string{"/web/config/web", "/web/config/save-web", "/web/config/php"} {
		rec := httptest.NewRecorder()
		if !h.checkWeakPasswordGuard(rec, httptest.NewRequest("GET", path, nil)) {
			t.Errorf("%s 应放行改密/只读配置", path)
		}
	}

	// 改成强口令后解除锁定
	config.CfgMu.Lock()
	config.Cfg.Web.Password = "Str0ng-Pass!2026"
	config.CfgMu.Unlock()
	rec := httptest.NewRecorder()
	if !h.checkWeakPasswordGuard(rec, httptest.NewRequest("POST", "/web/config/save-php", nil)) {
		t.Fatalf("强口令下应解除锁定")
	}
}

func TestLockedUnderWeakPassword(t *testing.T) {
	locked := []string{
		"config",
		"config/save",
		"config/save-php",
		"config/save-player",
		"config/save-server",
		"config/save-log",
		"config/save-global-auth",
		"config/backup/list",
		"config/backup/restore",
		"config/test-endpoint",
		"api/code/list",
		"api/code/save",
		"api/code/delete",
		"api/tasks/config/save",
		"api/tasks/run",
		"api/tasks/status",
		"api/sync/config/save",
		"api/github/config/save",
		"github/update",
		"github/releases",
		"api/backup/restore",
		"api/backup/download",
		"api/logs/stream",
		"api/dns/config/save",
	}
	for _, p := range locked {
		if !lockedUnderWeakPassword(p) {
			t.Errorf("lockedUnderWeakPassword(%q) 应锁定，实际放行", p)
		}
	}

	allowed := []string{
		"",
		"config/web",
		"config/save-web",
		"config/php",
		"config/player",
		"config/validate",
		"api/v1/elevate",
		"api/publisher/stats",
	}
	for _, p := range allowed {
		if lockedUnderWeakPassword(p) {
			t.Errorf("lockedUnderWeakPassword(%q) 应放行，实际锁定", p)
		}
	}
}
