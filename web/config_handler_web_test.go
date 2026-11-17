package web

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qist/tvgate/config"
)

// 修改密码必须校验当前密码：否则会话被劫持（或有人用公开初始口令登录）后可直接改密接管。
func TestSaveWebRequiresOldPassword(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	initial := "web:\n    enabled: true\n    username: ops-admin\n    password: \"Str0ng-Pass!2026\"\n    path: /secret-panel/\n"
	if err := os.WriteFile(cfgPath, []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}

	oldPath := *config.ConfigFilePath
	oldCfg := config.Cfg
	*config.ConfigFilePath = cfgPath
	config.CfgMu.Lock()
	config.Cfg.Web.Enabled = true
	config.Cfg.Web.Username = "ops-admin"
	config.Cfg.Web.Password = "Str0ng-Pass!2026"
	config.Cfg.Web.Path = "/secret-panel/"
	config.CfgMu.Unlock()
	t.Cleanup(func() {
		*config.ConfigFilePath = oldPath
		config.CfgMu.Lock()
		config.Cfg = oldCfg
		config.CfgMu.Unlock()
	})

	h := NewConfigHandler(WebConfig{Enabled: true, Username: "ops-admin", Password: "Str0ng-Pass!2026", Path: "/secret-panel/"})
	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/web/config/save-web", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "198.51.100.99:4321"
		h.handleWebConfigSave(rec, req)
		return rec
	}

	// 旧密码错误 → 403，且配置不变
	rec := post(`{"enabled":true,"username":"ops-admin","password":"NewStr0ng!2026","old_password":"wrong-pass","path":"/secret-panel/"}`)
	if rec.Code != 403 {
		t.Fatalf("旧密码错误应返回 403，实际 %d: %s", rec.Code, rec.Body.String())
	}
	if data, _ := os.ReadFile(cfgPath); !strings.Contains(string(data), "Str0ng-Pass!2026") {
		t.Fatalf("校验失败时不应修改口令:\n%s", data)
	}

	// 旧密码正确 → 200，新口令落盘
	rec = post(`{"enabled":true,"username":"ops-admin","password":"NewStr0ng!2026","old_password":"Str0ng-Pass!2026","path":"/secret-panel/"}`)
	if rec.Code != 200 {
		t.Fatalf("旧密码正确应保存成功，实际 %d: %s", rec.Code, rec.Body.String())
	}
	if data, _ := os.ReadFile(cfgPath); !strings.Contains(string(data), "NewStr0ng!2026") {
		t.Fatalf("新口令未写入配置:\n%s", data)
	}
}

// 未提交 password（部分更新）时不应清空现有口令。
func TestSaveWebPartialKeepsPassword(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	initial := "web:\n    enabled: true\n    username: ops-admin\n    password: \"Str0ng-Pass!2026\"\n    path: /secret-panel/\n"
	if err := os.WriteFile(cfgPath, []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}

	oldPath := *config.ConfigFilePath
	oldCfg := config.Cfg
	*config.ConfigFilePath = cfgPath
	config.CfgMu.Lock()
	config.Cfg.Web.Enabled = true
	config.Cfg.Web.Username = "ops-admin"
	config.Cfg.Web.Password = "Str0ng-Pass!2026"
	config.Cfg.Web.Path = "/secret-panel/"
	config.CfgMu.Unlock()
	t.Cleanup(func() {
		*config.ConfigFilePath = oldPath
		config.CfgMu.Lock()
		config.Cfg = oldCfg
		config.CfgMu.Unlock()
	})

	h := NewConfigHandler(WebConfig{Enabled: true, Username: "ops-admin", Password: "Str0ng-Pass!2026", Path: "/secret-panel/"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/web/config/save-web", strings.NewReader(`{"path":"/admin/"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "198.51.100.100:4321"
	h.handleWebConfigSave(rec, req)
	if rec.Code != 200 {
		t.Fatalf("部分更新应成功，实际 %d: %s", rec.Code, rec.Body.String())
	}
	data, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(data), "Str0ng-Pass!2026") {
		t.Fatalf("部分更新把口令清空了:\n%s", data)
	}
	if !strings.Contains(string(data), "/admin/") {
		t.Fatalf("路径未更新:\n%s", data)
	}
}
