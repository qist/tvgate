package phpgo

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestServePHP_LocationRedirect 验证 header('Location: ...') 应触发 302 重定向，
// 这样客户端（播放器/IPTV/curl -L）才会跟随到真实 m3u8 地址。
func TestServePHP_LocationRedirect(t *testing.T) {
	src := `<?php
header("Content-Type: application/vnd.apple.mpegurl");
header('Location: http://cdn.example.com/live/mnf.m3u8?bitrate=4000000');
exit;
`
	env := NewDefaultEnv(nil)
	rec := httptest.NewRecorder()
	if err := ServePHP(env, rec, src); err != nil {
		t.Fatalf("ServePHP error: %v", err)
	}
	if rec.Code != http.StatusFound {
		t.Fatalf("期望 302 Found，实际 %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "http://cdn.example.com/live/mnf.m3u8?bitrate=4000000" {
		t.Fatalf("Location 头错误: %q", loc)
	}
}

// TestServePHP_ExplicitStatus 验证显式 header("HTTP/1.1 404") 优先于 Location 约定。
func TestServePHP_ExplicitStatus(t *testing.T) {
	src := `<?php
header("HTTP/1.1 404 Not Found");
echo "missing";
`
	env := NewDefaultEnv(nil)
	rec := httptest.NewRecorder()
	if err := ServePHP(env, rec, src); err != nil {
		t.Fatalf("ServePHP error: %v", err)
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("期望 404，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "missing") {
		t.Fatalf("body 不应为空: %q", rec.Body.String())
	}
}

// TestServePHP_NoLocation200 验证普通 echo 仍返回 200。
func TestServePHP_NoLocation200(t *testing.T) {
	src := `<?php echo "hello";`
	env := NewDefaultEnv(nil)
	rec := httptest.NewRecorder()
	if err := ServePHP(env, rec, src); err != nil {
		t.Fatalf("ServePHP error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d", rec.Code)
	}
	if rec.Body.String() != "hello" {
		t.Fatalf("body 错误: %q", rec.Body.String())
	}
}
