package sync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManifestRoundTrip(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tvbox")
	os.MkdirAll(dir, 0755)

	m := &Manifest{Files: map[string]string{"a.txt": "sha1", "sub/b.txt": "sha2"}}
	if err := m.Save(dir, "owner/repo", "main"); err != nil {
		t.Fatal(err)
	}
	loaded := LoadManifest(dir)
	if loaded.Repo != "owner/repo" || loaded.Branch != "main" {
		t.Fatalf("meta mismatch: %+v", loaded)
	}
	if loaded.Files["a.txt"] != "sha1" || loaded.Files["sub/b.txt"] != "sha2" {
		t.Fatalf("files mismatch: %+v", loaded.Files)
	}
	// manifest 不应被当作同步文件读取
	if _, err := os.Stat(filepath.Join(dir, manifestName)); err != nil {
		t.Fatal("manifest file missing")
	}
}

func TestManifestCorrupt(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tvbox")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, manifestName), []byte("{not json"), 0644)
	m := LoadManifest(dir)
	if m.Files == nil || len(m.Files) != 0 {
		t.Fatalf("corrupt manifest should reset to empty: %+v", m.Files)
	}
}
