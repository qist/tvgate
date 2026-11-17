package web

import (
	"net/http/httptest"
	"testing"

	"github.com/qist/tvgate/config"
)

func TestCodeRootsAndSelection(t *testing.T) {
	oldDoc := config.Cfg.PHP.DocRoot
	oldAllowed := append([]string(nil), config.Cfg.PHP.AllowedRoots...)
	t.Cleanup(func() {
		config.CfgMu.Lock()
		config.Cfg.PHP.DocRoot = oldDoc
		config.Cfg.PHP.AllowedRoots = oldAllowed
		config.CfgMu.Unlock()
	})

	config.CfgMu.Lock()
	config.Cfg.PHP.DocRoot = "/srv/www"
	// /etc 是不安全目录，应被忽略
	config.Cfg.PHP.AllowedRoots = []string{"/srv/tvbox", "/etc"}
	config.CfgMu.Unlock()

	h := NewConfigHandler(WebConfig{Enabled: true, Path: "/web/"})
	roots := h.codeRoots()
	if len(roots) != 2 {
		t.Fatalf("codeRoots 长度 = %d, want 2（/etc 应被忽略）: %+v", len(roots), roots)
	}
	if roots[0].Path != "/srv/www" || roots[1].Path != "/srv/tvbox" {
		t.Fatalf("codeRoots 顺序不对: %+v", roots)
	}

	cases := []struct {
		query string
		want  string
		ok    bool
	}{
		{"", "/srv/www", true},          // 缺省 docroot
		{"?root=1", "/srv/tvbox", true}, // 附加目录
		{"?root=2", "", false},          // 越界
		{"?root=-1", "", false},         // 负数
		{"?root=abc", "", false},        // 非数字
		{"?root=/etc", "", false},       // 不接受任意路径
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "/web/api/code/list"+c.query, nil)
		got, err := h.codeRootForRequest(req)
		if c.ok {
			if err != nil || got != c.want {
				t.Errorf("codeRootForRequest(%q) = (%q,%v), want (%q,nil)", c.query, got, err, c.want)
			}
		} else if err == nil {
			t.Errorf("codeRootForRequest(%q) 应报错，实际返回 %q", c.query, got)
		}
	}
}
