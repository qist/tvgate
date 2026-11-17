package web

import (
	"bytes"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qist/tvgate/config"
)

func setTestDocRoot(t *testing.T, dir string) {
	t.Helper()
	oldDoc := config.Cfg.PHP.DocRoot
	oldAllowed := append([]string(nil), config.Cfg.PHP.AllowedRoots...)
	config.CfgMu.Lock()
	config.Cfg.PHP.DocRoot = dir
	config.Cfg.PHP.AllowedRoots = nil
	config.CfgMu.Unlock()
	t.Cleanup(func() {
		config.CfgMu.Lock()
		config.Cfg.PHP.DocRoot = oldDoc
		config.Cfg.PHP.AllowedRoots = oldAllowed
		config.CfgMu.Unlock()
	})
}

// 新建文件不能覆盖已存在文件（曾用 O_TRUNC 静默覆盖且无备份）
func TestHandleCodeNewRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	setTestDocRoot(t, dir)
	h := NewConfigHandler(WebConfig{Enabled: true, Path: "/web/"})

	existing := filepath.Join(dir, "keep.php")
	if err := os.WriteFile(existing, []byte("old-content"), 0644); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.handleCodeNew(rec, httptest.NewRequest("POST", "/web/api/code/new?path=keep.php&type=file", nil))
	if rec.Code != 409 {
		t.Fatalf("同名文件应返回 409，实际 %d: %s", rec.Code, rec.Body.String())
	}
	if data, _ := os.ReadFile(existing); string(data) != "old-content" {
		t.Fatalf("原文件被覆盖: %q", string(data))
	}

	// 新文件正常创建
	rec = httptest.NewRecorder()
	h.handleCodeNew(rec, httptest.NewRequest("POST", "/web/api/code/new?path=new.php&type=file", nil))
	if rec.Code != 200 {
		t.Fatalf("新建文件失败: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "new.php")); err != nil {
		t.Fatalf("新文件未创建: %v", err)
	}

	// 目录重复创建也拒绝
	rec = httptest.NewRecorder()
	h.handleCodeNew(rec, httptest.NewRequest("POST", "/web/api/code/new?path=sub&type=dir", nil))
	if rec.Code != 200 {
		t.Fatalf("新建目录失败: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.handleCodeNew(rec, httptest.NewRequest("POST", "/web/api/code/new?path=sub&type=dir", nil))
	if rec.Code != 409 {
		t.Fatalf("重复目录应返回 409，实际 %d", rec.Code)
	}

	// 确认覆盖（overwrite=true）：目录视为保持不变，返回 200
	rec = httptest.NewRecorder()
	h.handleCodeNew(rec, httptest.NewRequest("POST", "/web/api/code/new?path=sub&type=dir&overwrite=true", nil))
	if rec.Code != 200 {
		t.Fatalf("确认覆盖目录应返回 200，实际 %d: %s", rec.Code, rec.Body.String())
	}
}

// 确认覆盖（overwrite=true）必须先备份旧文件再写入
func TestHandleCodeNewOverwriteKeepsBackup(t *testing.T) {
	dir := t.TempDir()
	setTestDocRoot(t, dir)
	h := NewConfigHandler(WebConfig{Enabled: true, Path: "/web/"})

	existing := filepath.Join(dir, "keep.php")
	if err := os.WriteFile(existing, []byte("old-content"), 0644); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/web/api/code/new?path=keep.php&type=file&overwrite=true", strings.NewReader("new-content"))
	h.handleCodeNew(rec, req)
	if rec.Code != 200 {
		t.Fatalf("确认覆盖应返回 200，实际 %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"backed_up":true`) {
		t.Fatalf("响应未标记已备份: %s", rec.Body.String())
	}
	if data, _ := os.ReadFile(existing); string(data) != "new-content" {
		t.Fatalf("覆盖后内容不对: %q", data)
	}
	backups, _ := filepath.Glob(filepath.Join(dir, "keep.php.bak.*"))
	if len(backups) != 1 {
		t.Fatalf("应有 1 个备份文件，实际 %v", backups)
	}
	if data, _ := os.ReadFile(backups[0]); string(data) != "old-content" {
		t.Fatalf("备份内容应为旧内容，实际 %q", data)
	}
}

// 上传覆盖同名文件前必须自动备份
func TestHandleCodeUploadBacksUpExisting(t *testing.T) {
	dir := t.TempDir()
	setTestDocRoot(t, dir)

	existing := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(existing, []byte("old-content"), 0644); err != nil {
		t.Fatal(err)
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte("new-content")); err != nil {
		t.Fatal(err)
	}
	mw.Close()

	h := NewConfigHandler(WebConfig{Enabled: true, Path: "/web/"})
	req := httptest.NewRequest("POST", "/web/api/code/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.handleCodeUpload(rec, req)
	if rec.Code != 200 {
		t.Fatalf("上传失败: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"backed_up":["a.txt"]`) {
		t.Fatalf("响应未标记备份: %s", rec.Body.String())
	}
	if data, _ := os.ReadFile(existing); string(data) != "new-content" {
		t.Fatalf("上传内容未写入: %q", data)
	}
	backups, _ := filepath.Glob(filepath.Join(dir, "a.txt.bak.*"))
	if len(backups) != 1 {
		t.Fatalf("应有 1 个备份文件，实际 %v", backups)
	}
	if data, _ := os.ReadFile(backups[0]); string(data) != "old-content" {
		t.Fatalf("备份内容不对: %q", data)
	}
}
