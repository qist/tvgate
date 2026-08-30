package sync

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/qist/tvgate/config"
)

func TestGitLabTreePaginationAndFetch(t *testing.T) {
	var page1 []map[string]string
	for i := 0; i < 100; i++ {
		page1 = append(page1, map[string]string{
			"id":   "id" + strconv.Itoa(i),
			"path": "tvbox/f" + strconv.Itoa(i) + ".txt",
			"type": "blob",
		})
	}
	page2 := []map[string]string{
		{"id": "id100", "path": "tvbox/sub/last.txt", "type": "blob"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "glpat_test" {
			t.Errorf("missing PRIVATE-TOKEN")
		}
		switch {
		case r.URL.Path == "/api/v4/projects/owner/repo/repository/tree":
			page := r.URL.Query().Get("page")
			if page == "1" {
				w.Header().Set("X-Next-Page", "2")
				json.NewEncoder(w).Encode(page1)
			} else {
				json.NewEncoder(w).Encode(page2)
			}
		case r.URL.Path == "/api/v4/projects/owner/repo/repository/files/sub/last.txt/raw":
			w.Write([]byte("hello gitlab"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := NewGitLabClient(config.SyncConfig{Repo: "owner/repo", Token: "glpat_test"})
	c.host = srv.URL

	nodes, err := c.Tree("main", "tvbox")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 101 {
		t.Fatalf("nodes = %d, want 101", len(nodes))
	}
	if nodes[100].Path != "sub/last.txt" {
		t.Fatalf("last path = %q", nodes[100].Path)
	}

	content, err := c.Fetch("sub/last.txt", "main")
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello gitlab" {
		t.Fatalf("content = %q", content)
	}
}
