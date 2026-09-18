package load

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qist/tvgate/config"
)

// 回归：LoadConfig **发布前**必须补齐默认值。HTTP 等子结构含 *bool 字段
// （insecure_skip_verify / disable_keepalives），YAML 没写这些键时 Unmarshal 出来是 nil；
// 若先把 nil 版配置发布到 config.Cfg，再等调用方 SetDefaults，中间这段窗口里任何并发读取
// （新建 HTTP client、DNS 解析）都会空指针 panic 直接崩进程 —— 实测后台保存配置后进程被带走。
// 另外 web 各保存路径（saveAndResponse）发布后并不会再调 SetDefaults，nil 会一直留着，
// 所以这里断言"发布出去的配置一定没有 nil 指针"。
func TestLoadConfigPublishesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("log:\n  enabled: false\nplayer:\n  enabled: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 生产环境里这两个是同一个路径（-config 参数）；这里一并对齐，顺便验证
	// 相对 docroot 是按"配置文件所在目录"解析的。
	oldPath := *config.ConfigFilePath
	*config.ConfigFilePath = path
	t.Cleanup(func() { *config.ConfigFilePath = oldPath })

	if err := LoadConfig(path); err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	if config.Cfg.HTTP.InsecureSkipVerify == nil {
		t.Fatal("发布出去的配置 insecure_skip_verify 仍是 nil → 并发建 client 会 panic")
	}
	if config.Cfg.HTTP.DisableKeepAlives == nil {
		t.Fatal("发布出去的配置 disable_keepalives 仍是 nil → 并发建 client 会 panic")
	}
	if *config.Cfg.HTTP.DisableKeepAlives {
		t.Fatal("未配置时应为 false（保持长连接）")
	}
	if config.Cfg.Player.Enabled != true {
		t.Fatalf("player 段未生效: %+v", config.Cfg.Player)
	}
	// 相对 docroot 解析成绝对路径、基准为配置文件所在目录（SetDefaults 的一部分，必须已生效）
	if want := filepath.Join(dir, "www"); config.Cfg.PHP.DocRoot != want {
		t.Fatalf("docroot 解析不对: got %q want %q", config.Cfg.PHP.DocRoot, want)
	}
}
