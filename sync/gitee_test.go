package sync

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qist/tvgate/config"
)

// testGitee 构造指向 httptest 的 GiteeClient
func testGitee(srv *httptest.Server) *GiteeClient {
	return NewGiteeClient(config.SyncConfig{
		Repo: "owner/repo", Branch: "master", Token: "gitee_token",
		Host: srv.URL, Timeout: 0,
	})
}

// buildZipArchive 构造带顶层目录的 zip 归档（模拟 Gitee 归档）
func buildZipArchive(content map[string]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for path, c := range content {
		w, err := zw.Create("repo-topdir/" + path)
		if err != nil {
			return nil
		}
		if _, err := w.Write([]byte(c)); err != nil {
			return nil
		}
	}
	zw.Close()
	return buf.Bytes()
}

func TestGiteeTreeAndFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("access_token") != "gitee_token" {
			t.Errorf("missing access_token")
		}
		switch {
		case r.URL.Path == "/api/v5/repos/owner/repo/branches/master":
			json.NewEncoder(w).Encode(map[string]any{"name": "master", "commit": map[string]any{"sha": "abc123"}})
		case r.URL.Path == "/api/v5/repos/owner/repo/git/trees/abc123":
			json.NewEncoder(w).Encode(map[string]any{
				"tree": []map[string]string{
					{"path": "tvbox", "type": "tree", "sha": "t1"},
					{"path": "tvbox/a.php", "type": "blob", "sha": "b1"},
					{"path": "tvbox/sub/b.txt", "type": "blob", "sha": "b2"},
				},
			})
		case r.URL.Path == "/api/v5/repos/owner/repo/git/blobs/b1":
			enc := base64.StdEncoding.EncodeToString([]byte("<?php echo 1;"))
			json.NewEncoder(w).Encode(map[string]string{"content": enc, "encoding": "base64"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := testGitee(srv)
	nodes, err := c.Tree("master", "tvbox")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0].Path != "a.php" || nodes[1].Path != "sub/b.txt" {
		t.Fatalf("nodes = %+v", nodes)
	}
	content, err := c.Fetch("tvbox/a.php", "b1")
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "<?php echo 1;" {
		t.Fatalf("content = %q", content)
	}
}

func TestGiteeArchiveZip(t *testing.T) {
	zipData := buildZipArchive(map[string]string{"a.txt": "hello", "sub/b.txt": "world"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/owner/repo/repository/archive/master.zip" {
			if r.URL.Query().Get("access_token") != "gitee_token" {
				t.Errorf("missing access_token")
			}
			w.Write(zipData)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := testGitee(srv)
	data, err := c.Archive("master")
	if err != nil {
		t.Fatal(err)
	}
	files, err := extractArchive(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || string(files["a.txt"]) != "hello" || string(files["sub/b.txt"]) != "world" {
		t.Fatalf("zip extract mismatch: %v", files)
	}
}

func TestExtractArchiveUnrecognized(t *testing.T) {
	if _, err := extractArchive([]byte("not an archive")); err == nil {
		t.Fatal("expected error for unrecognized format")
	}
}
