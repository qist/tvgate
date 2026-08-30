package sync

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qist/tvgate/config"
)

// testGitHub 构造指向 httptest 的 GitHubClient（不启用加速，走官方路径 = 测试服务器）
func testGitHub(srv *httptest.Server) *GitHubClient {
	c := NewGitHubClient(config.SyncConfig{Repo: "owner/repo", Token: "ghp_test", Timeout: 0}, config.GithubConfig{})
	c.baseURL = srv.URL
	return c
}

func TestGitHubTreeAndFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 校验 Authorization 透传
		if r.Header.Get("Authorization") != "Bearer ghp_test" {
			t.Errorf("missing bearer token")
		}
		switch {
		case r.URL.Path == "/repos/owner/repo/git/trees/main":
			json.NewEncoder(w).Encode(map[string]any{
				"tree": []map[string]string{
					{"path": "tvbox", "type": "tree", "sha": "t1"},
					{"path": "tvbox/a.php", "type": "blob", "sha": "b1"},
					{"path": "tvbox/sub/b.txt", "type": "blob", "sha": "b2"},
					{"path": "other/c.txt", "type": "blob", "sha": "b3"},
				},
			})
		case r.URL.Path == "/repos/owner/repo/git/blobs/b1":
			enc := base64.StdEncoding.EncodeToString([]byte("<?php echo 1;"))
			json.NewEncoder(w).Encode(map[string]string{"content": enc, "encoding": "base64"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := testGitHub(srv)

	nodes, err := c.Tree("main", "tvbox")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes = %d, want 2: %+v", len(nodes), nodes)
	}
	// 已去掉 tvbox/ 前缀
	if nodes[0].Path != "a.php" || nodes[1].Path != "sub/b.txt" {
		t.Fatalf("paths not stripped: %+v", nodes)
	}

	content, err := c.Fetch("a.php", "b1")
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "<?php echo 1;" {
		t.Fatalf("content = %q", content)
	}
}

func TestStripPrefix(t *testing.T) {
	cases := []struct {
		path, prefix string
		want         string
		ok           bool
	}{
		{"a/b.txt", "", "a/b.txt", true},
		{"a/b.txt", ".", "a/b.txt", true},
		{"tvbox/a.php", "tvbox", "a.php", true},
		{"tvbox", "tvbox", "", false},
		{"other/c.txt", "tvbox", "", false},
	}
	for _, c := range cases {
		got, ok := stripPrefix(c.path, c.prefix)
		if got != c.want || ok != c.ok {
			t.Errorf("stripPrefix(%q, %q) = %q, %v; want %q, %v", c.path, c.prefix, got, ok, c.want, c.ok)
		}
	}
}
