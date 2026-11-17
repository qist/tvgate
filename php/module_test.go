package php

import "testing"

func TestIsBlockedPHPPath(t *testing.T) {
	blocked := []string{
		"a.php.bak.20260101120000.123",
		"sub/x.php.bak.1",
		".git/config",
		".env",
		"a/.htaccess",
		".svn/entries",
		"a/.hidden/b.php",
	}
	for _, p := range blocked {
		if !isBlockedPHPPath(p) {
			t.Errorf("isBlockedPHPPath(%q) 应被拒绝", p)
		}
	}

	allowed := []string{
		"",
		"index.php",
		"sub/a.php",
		".well-known/acme-challenge/token",
		"a/b/c.js",
	}
	for _, p := range allowed {
		if isBlockedPHPPath(p) {
			t.Errorf("isBlockedPHPPath(%q) 应放行", p)
		}
	}
}

func TestPathWithin(t *testing.T) {
	cases := []struct {
		p, root string
		want    bool
	}{
		{"/srv/www", "/srv/www", true},
		{"/srv/www/a.php", "/srv/www", true},
		{"/srv/www2/a.php", "/srv/www", false}, // 同前缀兄弟目录不算
		{"/srv/ww", "/srv/www", false},
		{"", "/srv/www", false},
		{"/srv/www", "", false},
	}
	for _, c := range cases {
		if got := pathWithin(c.p, c.root); got != c.want {
			t.Errorf("pathWithin(%q,%q) = %v, want %v", c.p, c.root, got, c.want)
		}
	}
}
