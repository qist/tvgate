package web

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestReplaceSyncConfigNodeTag 验证手工构造 YAML 节点时显式设置 Tag，
// 避免序列化出 `repo: !!null qist/tvbox` 导致配置重新加载失败（回归测试）。
func TestReplaceSyncConfigNodeTag(t *testing.T) {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte("server:\n  port: 8888\n"), &node); err != nil {
		t.Fatal(err)
	}
	entry := map[string]interface{}{
		"name":       "tvbox",
		"enabled":    true,
		"type":       "github",
		"repo":       "qist/tvbox",
		"branch":     "master",
		"token":      "",
		"interval":   "60s",
		"repo_path":  ".",
		"local_path": "tvbox",
		"only_php":   false,
		"backup":     true,
		"delete":     false,
		"timeout":    "15s",
		"protect":    []interface{}{"tv.txt", "private/"},
	}
	if err := replaceSyncConfigNode(&node, []map[string]interface{}{entry}); err != nil {
		t.Fatal(err)
	}
	out, err := yaml.Marshal(&node)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "!!null") {
		t.Fatalf("输出包含错误的 !!null 标签:\n%s", s)
	}
	// 重新解析必须成功，且值正确
	var cfg struct {
		Sync []struct {
			Name      string   `yaml:"name"`
			Enabled   bool     `yaml:"enabled"`
			Repo      string   `yaml:"repo"`
			Branch    string   `yaml:"branch"`
			LocalPath string   `yaml:"local_path"`
			Protect   []string `yaml:"protect"`
		} `yaml:"sync"`
	}
	if err := yaml.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("重新解析失败: %v\n%s", err, s)
	}
	if len(cfg.Sync) != 1 {
		t.Fatalf("sync 条目数 = %d, want 1", len(cfg.Sync))
	}
	got := cfg.Sync[0]
	if got.Name != "tvbox" || got.Repo != "qist/tvbox" || got.Branch != "master" || !got.Enabled || got.LocalPath != "tvbox" {
		t.Fatalf("字段不匹配: %+v", got)
	}
	if len(got.Protect) != 2 || got.Protect[0] != "tv.txt" {
		t.Fatalf("protect 不匹配: %+v", got.Protect)
	}
}

// TestReplaceSyncConfigNodeMulti 验证多仓库列表保存
func TestReplaceSyncConfigNodeMulti(t *testing.T) {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte("server:\n  port: 8888\n"), &node); err != nil {
		t.Fatal(err)
	}
	entries := []map[string]interface{}{
		{"name": "tvbox", "enabled": true, "repo": "qist/tvbox", "local_path": "tvbox"},
		{"name": "php", "enabled": false, "repo": "qist/php-scripts", "local_path": "www/scripts"},
	}
	if err := replaceSyncConfigNode(&node, entries); err != nil {
		t.Fatal(err)
	}
	out, err := yaml.Marshal(&node)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Sync []struct {
			Name      string `yaml:"name"`
			Repo      string `yaml:"repo"`
			LocalPath string `yaml:"local_path"`
		} `yaml:"sync"`
	}
	if err := yaml.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("重新解析失败: %v\n%s", err, string(out))
	}
	if len(cfg.Sync) != 2 {
		t.Fatalf("sync 条目数 = %d, want 2", len(cfg.Sync))
	}
	if cfg.Sync[0].Repo != "qist/tvbox" || cfg.Sync[1].Repo != "qist/php-scripts" {
		t.Fatalf("条目不匹配: %+v", cfg.Sync)
	}
}
