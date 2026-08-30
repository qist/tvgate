package sync

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/qist/tvgate/config"
)

// fakeClient 内存版 RepoClient，便于测试同步主流程
type fakeClient struct {
	content map[string]string // relPath -> content
	sha     map[string]string // relPath -> sha
}

func newFakeClient(m map[string]string) *fakeClient {
	f := &fakeClient{content: map[string]string{}, sha: map[string]string{}}
	for path, content := range m {
		f.content[path] = content
		f.sha[path] = computeGitBlobSHA([]byte(content)) // 与归档模式本地计算一致
	}
	return f
}

func (f *fakeClient) Tree(branch, prefix string) ([]FileNode, error) {
	var nodes []FileNode
	for path := range f.content {
		rel, ok := stripPrefix(path, prefix)
		if !ok {
			continue
		}
		nodes = append(nodes, FileNode{Path: rel, SHA: f.sha[path]})
	}
	return nodes, nil
}

func (f *fakeClient) Fetch(path, ref string) ([]byte, error) {
	return []byte(f.content[path]), nil
}

func (f *fakeClient) Archive(branch string) ([]byte, error) {
	// 构造与 content 等价的 tar.gz 归档，模拟整仓下载
	return buildFakeArchive(f.content), nil
}

func (f *fakeClient) RepoID() string { return "owner/repo" }

// buildFakeArchive 将 map 打包为带顶层目录前缀的 tar.gz（与 GitHub 归档结构一致：topdir/<path>）
func buildFakeArchive(content map[string]string) []byte {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for path, c := range content {
		hdr := &tar.Header{
			Name: "repo-topdir/" + path,
			Mode: 0644,
			Size: int64(len(c)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil
		}
		if _, err := tw.Write([]byte(c)); err != nil {
			return nil
		}
	}
	tw.Close()
	gw.Close()
	return buf.Bytes()
}

func newTestManager(localRoot string, fc *fakeClient, delete bool) *SyncManager {
	return &SyncManager{
		cfg:       &config.SyncConfig{Branch: "main", Delete: boolPtr(delete)},
		client:    fc,
		localRoot: localRoot,
	}
}

func TestSyncOnceFirstAndIncremental(t *testing.T) {
	root := t.TempDir()
	localRoot := filepath.Join(root, "tvbox")
	os.MkdirAll(localRoot, 0755)

	fc := newFakeClient(map[string]string{"a.php": "<?php echo 1;", "sub/b.txt": "bbb"})
	sm := newTestManager(localRoot, fc, true)

	// 首次全量
	if err := sm.syncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(localRoot, "a.php")); string(b) != "<?php echo 1;" {
		t.Fatalf("a.php = %q", b)
	}
	m := LoadManifest(localRoot)
	if m.Files["sub/b.txt"] != fc.sha["sub/b.txt"] {
		t.Fatalf("manifest sha mismatch")
	}

	// 增量：内容未变 → 跳过（文件修改时间不应变化）
	fi1, _ := os.Stat(filepath.Join(localRoot, "a.php"))
	sm.syncOnce(context.Background())
	fi2, _ := os.Stat(filepath.Join(localRoot, "a.php"))
	if !fi1.ModTime().Equal(fi2.ModTime()) {
		t.Fatal("unchanged file should not be rewritten")
	}

	// 增量：修改一个文件 → 只更新该文件
	fc.content["sub/b.txt"] = "bbb2"
	fc.sha["sub/b.txt"] = computeGitBlobSHA([]byte("bbb2"))
	if err := sm.syncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(localRoot, "sub/b.txt")); string(b) != "bbb2" {
		t.Fatalf("sub/b.txt = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(localRoot, "a.php")); string(b) != "<?php echo 1;" {
		t.Fatalf("a.php should be untouched, got %q", b)
	}
}

func TestSyncOnceDelete(t *testing.T) {
	root := t.TempDir()
	localRoot := filepath.Join(root, "tvbox")
	os.MkdirAll(localRoot, 0755)

	fc := newFakeClient(map[string]string{"a.txt": "v1"})
	sm := newTestManager(localRoot, fc, true)
	sm.syncOnce(context.Background())

	// 远端删除 a.txt，delete=true → 本地删除
	fc.content = map[string]string{}
	if err := sm.syncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(localRoot, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("a.txt should be deleted")
	}
}

func TestSyncOnceProtect(t *testing.T) {
	root := t.TempDir()
	localRoot := filepath.Join(root, "tvbox")
	os.MkdirAll(localRoot, 0755)

	fc := newFakeClient(map[string]string{"a.txt": "v1"})
	sm := newTestManager(localRoot, fc, true)
	sm.syncOnce(context.Background())

	// 本地私有化 a.txt（手工编辑），加入 protect
	os.WriteFile(filepath.Join(localRoot, "a.txt"), []byte("local edit"), 0644)
	sm.cfg.Protect = []string{"a.txt"}

	// 远端删除 a.txt → protect 内不删除
	fc.content = map[string]string{}
	sm.syncOnce(context.Background())
	if b, _ := os.ReadFile(filepath.Join(localRoot, "a.txt")); string(b) != "local edit" {
		t.Fatalf("protected file should be kept: %q", b)
	}

	// 远端重新出现且内容不同 → protect 内不覆盖
	fc.content = map[string]string{"a.txt": "v2"}
	fc.sha["a.txt"] = computeGitBlobSHA([]byte("v2"))
	sm.syncOnce(context.Background())
	if b, _ := os.ReadFile(filepath.Join(localRoot, "a.txt")); string(b) != "local edit" {
		t.Fatalf("protected file should not be overwritten: %q", b)
	}
}

func TestSyncOncePHPInvalid(t *testing.T) {
	root := t.TempDir()
	localRoot := filepath.Join(root, "tvbox")
	os.MkdirAll(localRoot, 0755)

	fc := newFakeClient(map[string]string{"bad.php": "<?php echo \"unterminated;"})
	sm := newTestManager(localRoot, fc, true)
	if err := sm.syncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(localRoot, "bad.php")); !os.IsNotExist(err) {
		t.Fatal("invalid php should not be written")
	}
}

// TestSyncOnceArchive 变更文件数超过阈值时走整仓归档路径，全部文件正确落盘且 manifest 记录
func TestSyncOnceArchive(t *testing.T) {
	root := t.TempDir()
	localRoot := filepath.Join(root, "tvbox")
	os.MkdirAll(localRoot, 0755)

	// 构造 > archiveThreshold 个文件，确保触发归档下载
	content := map[string]string{}
	for i := 0; i < archiveThreshold+10; i++ {
		content[fmt.Sprintf("f%02d.txt", i)] = fmt.Sprintf("content-%d", i)
	}
	content["sub/nested.txt"] = "nested"
	fc := newFakeClient(content)
	sm := newTestManager(localRoot, fc, true)

	if err := sm.syncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < archiveThreshold+10; i++ {
		p := filepath.Join(localRoot, fmt.Sprintf("f%02d.txt", i))
		if b, _ := os.ReadFile(p); string(b) != fmt.Sprintf("content-%d", i) {
			t.Fatalf("%s = %q", p, b)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(localRoot, "sub/nested.txt")); string(b) != "nested" {
		t.Fatalf("nested = %q", b)
	}
	m := LoadManifest(localRoot)
	if len(m.Files) != archiveThreshold+11 {
		t.Fatalf("manifest files = %d, want %d", len(m.Files), archiveThreshold+11)
	}

	// 增量：内容不变 → 全部跳过（不再触发归档下载，且文件不变）
	sm.syncOnce(context.Background())
	m2 := LoadManifest(localRoot)
	if len(m2.Files) != archiveThreshold+11 {
		t.Fatalf("second pass manifest files = %d", len(m2.Files))
	}
}

// TestExtractArchive 解析真实结构归档（顶层目录 + 子目录）
func TestExtractArchive(t *testing.T) {
	content := map[string]string{
		"a.txt":      "hello",
		"sub/b.txt":  "world",
		"sub/deep/c": "deep",
	}
	archive := buildFakeArchive(content)
	files, err := extractArchive(archive)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("files = %d, want 3: %v", len(files), files)
	}
	if string(files["a.txt"]) != "hello" || string(files["sub/b.txt"]) != "world" || string(files["sub/deep/c"]) != "deep" {
		t.Fatalf("content mismatch: %v", files)
	}
}
