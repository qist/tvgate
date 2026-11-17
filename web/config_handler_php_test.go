package web

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qist/tvgate/config"
)

// 部分更新保护：只提交 docroot 时，其它 php 字段必须保留（不能被整段清空）。
func TestPHPConfigSavePartialKeepsOtherFields(t *testing.T) {
	dir := t.TempDir()
	docroot := filepath.Join(dir, "www")
	if err := os.MkdirAll(docroot, 0755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	initial := "php:\n" +
		"    enabled: true\n" +
		"    path: /php/\n" +
		"    docroot: " + docroot + "\n" +
		"    index:\n" +
		"        - index.php\n" +
		"        - index.html\n" +
		"    worker_mode: false\n" +
		"    workers: 4\n"
	if err := os.WriteFile(cfgPath, []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}

	oldPath := *config.ConfigFilePath
	*config.ConfigFilePath = cfgPath
	oldPHP := config.Cfg.PHP
	config.CfgMu.Lock()
	config.Cfg.PHP.Enabled = true
	config.Cfg.PHP.Path = "/php/"
	config.Cfg.PHP.DocRoot = docroot
	config.Cfg.PHP.Index = []string{"index.php", "index.html"}
	config.Cfg.PHP.Workers = 4
	config.Cfg.PHP.WorkerMode = false
	config.Cfg.PHP.AllowedRoots = nil
	config.CfgMu.Unlock()
	t.Cleanup(func() {
		*config.ConfigFilePath = oldPath
		config.CfgMu.Lock()
		config.Cfg.PHP = oldPHP
		config.CfgMu.Unlock()
	})

	h := NewConfigHandler(WebConfig{Enabled: true, Path: "/web/"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/web/config/save-php", strings.NewReader(`{"docroot":"`+docroot+`"}`))
	req.Header.Set("Content-Type", "application/json")
	h.handlePHPConfigSave(rec, req)
	if rec.Code != 200 {
		t.Fatalf("保存失败: %d %s", rec.Code, rec.Body.String())
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{"enabled: true", "path: /php/", "index.php", "workers: 4", "docroot: " + docroot} {
		if !strings.Contains(content, want) {
			t.Errorf("保存后配置缺少 %q，实际:\n%s", want, content)
		}
	}
}
