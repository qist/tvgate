package sync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qist/tvgate/config"
)

func boolPtr(b bool) *bool { return &b }

func TestIsProtected(t *testing.T) {
	protect := []string{"tv.txt", "private/"}
	cases := []struct {
		rel  string
		want bool
	}{
		{"tv.txt", true},
		{"private/a.txt", true},
		{"private", true},
		{"a/tv.txt", false},
		{"tv.txt.bak", false},
		{"other.txt", false},
	}
	for _, c := range cases {
		if got := isProtected(protect, c.rel); got != c.want {
			t.Errorf("isProtected(%v, %q) = %v, want %v", protect, c.rel, got, c.want)
		}
	}
}

func TestSafeLocalPath(t *testing.T) {
	localRoot := "/data/www/tvbox"
	if _, err := safeLocalPath(localRoot, "../evil.txt"); err == nil {
		t.Error("expected traversal to be rejected")
	}
	if _, err := safeLocalPath(localRoot, "/abs/path"); err == nil {
		t.Error("expected absolute path to be rejected")
	}
	p, err := safeLocalPath(localRoot, "sub/a.txt")
	if err != nil || p != "/data/www/tvbox/sub/a.txt" {
		t.Errorf("safeLocalPath = %q, %v", p, err)
	}
}

func TestApplyUpdateAndDelete(t *testing.T) {
	root := t.TempDir()
	localRoot := filepath.Join(root, "tvbox")
	os.MkdirAll(localRoot, 0755)

	cfg := &config.SyncConfig{Backup: boolPtr(true), Delete: boolPtr(true)}

	// 新增
	res := &ApplyResult{}
	if err := ApplyUpdate(localRoot, cfg, "sub/a.txt", []byte("hello"), res); err != nil {
		t.Fatal(err)
	}
	if res.Added != 1 || res.Updated != 0 {
		t.Fatalf("add: added=%d updated=%d", res.Added, res.Updated)
	}
	if b, _ := os.ReadFile(filepath.Join(localRoot, "sub/a.txt")); string(b) != "hello" {
		t.Fatalf("content = %q", b)
	}

	// 覆盖 + 备份
	res = &ApplyResult{}
	if err := ApplyUpdate(localRoot, cfg, "sub/a.txt", []byte("world"), res); err != nil {
		t.Fatal(err)
	}
	if res.Added != 0 || res.Updated != 1 {
		t.Fatalf("update: added=%d updated=%d", res.Added, res.Updated)
	}
	if b, _ := os.ReadFile(filepath.Join(localRoot, "sub/a.txt")); string(b) != "world" {
		t.Fatalf("content = %q", b)
	}
	baks, _ := filepath.Glob(filepath.Join(localRoot, "sub", "a.txt.bak.*"))
	if len(baks) != 1 {
		t.Fatalf("backup not created: %v", baks)
	}

	// 删除
	res = &ApplyResult{}
	if err := ApplyDelete(localRoot, cfg, "sub/a.txt", res); err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 1 {
		t.Fatalf("deleted = %d", res.Deleted)
	}
	if _, err := os.Stat(filepath.Join(localRoot, "sub/a.txt")); !os.IsNotExist(err) {
		t.Fatal("file should be deleted")
	}
}

func TestApplyUpdatePHPInvalid(t *testing.T) {
	root := t.TempDir()
	localRoot := filepath.Join(root, "tvbox")
	os.MkdirAll(localRoot, 0755)
	cfg := &config.SyncConfig{}

	// 未闭合引号 → 拒绝覆盖
	res := &ApplyResult{}
	ApplyUpdate(localRoot, cfg, "bad.php", []byte("<?php echo \"unterminated;"), res)
	if len(res.Errors) == 0 {
		t.Fatal("expected PHP check to reject")
	}
	if _, err := os.Stat(filepath.Join(localRoot, "bad.php")); !os.IsNotExist(err) {
		t.Fatal("invalid php should not be written")
	}

	// 非 PHP 文件不过校验，直接写
	res = &ApplyResult{}
	if err := ApplyUpdate(localRoot, cfg, "a.txt", []byte("plain"), res); err != nil {
		t.Fatal(err)
	}
	if res.Added != 1 {
		t.Fatalf("added = %d", res.Added)
	}
}
