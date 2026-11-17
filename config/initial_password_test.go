package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func setupBootstrapConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if content != "" {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	old := *ConfigFilePath
	*ConfigFilePath = path
	t.Cleanup(func() {
		*ConfigFilePath = old
		_ = os.Remove(filepath.Join(dir, initialPasswordFileName))
	})
	return path
}

type persistedWeb struct {
	Username string
	Password string
	Path     string
	Enabled  bool
}

func readPersistedWeb(t *testing.T, path string) persistedWeb {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取配置失败: %v", err)
	}
	var doc struct {
		Web struct {
			Username string `yaml:"username"`
			Password string `yaml:"password"`
			Path     string `yaml:"path"`
			Enabled  bool   `yaml:"enabled"`
		} `yaml:"web"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("解析配置失败: %v", err)
	}
	return persistedWeb{
		Username: doc.Web.Username,
		Password: doc.Web.Password,
		Path:     doc.Web.Path,
		Enabled:  doc.Web.Enabled,
	}
}

func newDefaultWebConfig() *Config {
	c := &Config{}
	c.Web.Enabled = true
	c.Web.Username = "admin"
	c.Web.Password = "admin"
	c.Web.Path = "/web/"
	return c
}

// 三项都是公开默认值：全部随机化，并写回配置 + 生成 0600 凭据文件。
func TestEnsureWebPasswordBootstrapReplacesDefaults(t *testing.T) {
	path := setupBootstrapConfig(t, "web:\n    enabled: true\n    username: admin\n    password: admin\n    path: /web/\n")
	c := newDefaultWebConfig()

	res := EnsureWebPasswordBootstrap(c)
	if res == nil {
		t.Fatal("默认值应触发随机化")
	}
	if strings.EqualFold(c.Web.Username, "admin") {
		t.Fatalf("用户名未随机化: %q", c.Web.Username)
	}
	if normalizedWebPath(c.Web.Path) == "/web/" {
		t.Fatalf("路径未随机化: %q", c.Web.Path)
	}
	if len([]rune(c.Web.Password)) != initialPasswordLength {
		t.Fatalf("口令长度不对: %q", c.Web.Password)
	}

	doc := readPersistedWeb(t, path)
	if doc.Username != c.Web.Username || doc.Password != c.Web.Password || doc.Path != normalizedWebPath(c.Web.Path) {
		t.Fatalf("配置未写回: %+v", doc)
	}
	if !doc.Enabled {
		t.Fatal("enabled 应保持 true")
	}

	fp := InitialPasswordFile()
	info, err := os.Stat(fp)
	if err != nil {
		t.Fatalf("凭据文件未生成: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("凭据文件权限 = %o, want 600", perm)
	}
	data, _ := os.ReadFile(fp)
	for _, want := range []string{c.Web.Username, c.Web.Password, normalizedWebPath(c.Web.Path)} {
		if !strings.Contains(string(data), want) {
			t.Errorf("凭据文件缺少 %q:\n%s", want, data)
		}
	}
	if !strings.Contains(res.Banner(), c.Web.Password) || !strings.Contains(res.Banner(), normalizedWebPath(c.Web.Path)) {
		t.Errorf("Banner 未包含口令或路径")
	}
}

// 三项全部自定义：完全不动，且清理遗留的凭据文件。
func TestEnsureWebPasswordBootstrapKeepsFullyCustom(t *testing.T) {
	path := setupBootstrapConfig(t, "web:\n    enabled: true\n    username: ops-admin\n    password: \"Str0ng-Pass!2026\"\n    path: /secret-panel/\n")
	_ = os.WriteFile(InitialPasswordFile(), []byte("username: ops-admin\npassword: OldInitPass1!\npath: /old/\n"), 0o600)

	c := &Config{}
	c.Web.Enabled = true
	c.Web.Username = "ops-admin"
	c.Web.Password = "Str0ng-Pass!2026"
	c.Web.Path = "/secret-panel/"

	if res := EnsureWebPasswordBootstrap(c); res != nil {
		t.Fatalf("三项都自定义时不应改动，实际 %+v", res)
	}
	if c.Web.Username != "ops-admin" || c.Web.Password != "Str0ng-Pass!2026" || c.Web.Path != "/secret-panel/" {
		t.Fatalf("配置被意外修改: %+v", c.Web)
	}
	doc := readPersistedWeb(t, path)
	if doc.Password != "Str0ng-Pass!2026" || doc.Path != "/secret-panel/" {
		t.Fatalf("配置文件被意外修改: %+v", doc)
	}
	if InitialPasswordFileExists() {
		t.Fatal("过期凭据文件应被清理")
	}
}

// 口令已自定义：用户名/路径即使是默认值也保持不动（老部署 URL 不受影响）。
func TestEnsureWebPasswordBootstrapKeepsDefaultsWhenPasswordCustom(t *testing.T) {
	path := setupBootstrapConfig(t, "web:\n    enabled: true\n    username: admin\n    password: \"Str0ng-Pass!2026\"\n    path: /web/\n")

	c := &Config{}
	c.Web.Enabled = true
	c.Web.Username = "admin"
	c.Web.Password = "Str0ng-Pass!2026"
	c.Web.Path = "/web/"

	if res := EnsureWebPasswordBootstrap(c); res != nil {
		t.Fatalf("口令已自定义时不应做任何改动，实际 %+v", res)
	}
	if c.Web.Username != "admin" || c.Web.Path != "/web/" || c.Web.Password != "Str0ng-Pass!2026" {
		t.Fatalf("配置被意外修改: %+v", c.Web)
	}
	doc := readPersistedWeb(t, path)
	if doc.Username != "admin" || doc.Path != "/web/" || doc.Password != "Str0ng-Pass!2026" {
		t.Fatalf("配置文件被意外修改: %+v", doc)
	}
	if InitialPasswordFileExists() {
		t.Fatal("口令已自定义时不应生成凭据文件")
	}
}

// 口令是默认值、但用户名/路径已自定义：只轮换口令，保留用户名与路径。
func TestEnsureWebPasswordBootstrapKeepsCustomIdentity(t *testing.T) {
	path := setupBootstrapConfig(t, "web:\n    enabled: true\n    username: ops-admin\n    password: admin\n    path: /secret-panel/\n")

	c := &Config{}
	c.Web.Enabled = true
	c.Web.Username = "ops-admin"
	c.Web.Password = "admin"
	c.Web.Path = "/secret-panel/"

	res := EnsureWebPasswordBootstrap(c)
	if res == nil {
		t.Fatal("默认口令应触发初始化")
	}
	if c.Web.Username != "ops-admin" || c.Web.Path != "/secret-panel/" {
		t.Fatalf("已自定义的用户名/路径不应被修改: %+v", c.Web)
	}
	if c.Web.Password == "admin" || len([]rune(c.Web.Password)) != initialPasswordLength {
		t.Fatalf("口令应换为随机值: %q", c.Web.Password)
	}
	doc := readPersistedWeb(t, path)
	if doc.Username != "ops-admin" || doc.Path != "/secret-panel/" || doc.Password != c.Web.Password {
		t.Fatalf("配置未正确写回: %+v", doc)
	}
}

// 环境变量可指定口令；用户名/路径仍随机化。过弱的环境变量被忽略。
func TestEnsureWebPasswordBootstrapEnv(t *testing.T) {
	setupBootstrapConfig(t, "web:\n    enabled: true\n    username: admin\n    password: admin\n    path: /web/\n")

	t.Setenv(EnvWebAdminPassword, "FromEnv-Pass-2026")
	c := newDefaultWebConfig()
	res := EnsureWebPasswordBootstrap(c)
	if res == nil || c.Web.Password != "FromEnv-Pass-2026" {
		t.Fatalf("应使用环境变量口令，实际 %q (res=%+v)", c.Web.Password, res)
	}
	if strings.EqualFold(c.Web.Username, "admin") {
		t.Error("用户名仍应随机化")
	}

	t.Setenv(EnvWebAdminPassword, "admin")
	c2 := newDefaultWebConfig()
	res2 := EnsureWebPasswordBootstrap(c2)
	if res2 == nil || c2.Web.Password == "admin" {
		t.Fatalf("弱环境变量应被忽略并生成随机口令，实际 %q", c2.Web.Password)
	}
}

// 配置文件不可写时（写回失败），下次加载应复用文件里的凭据，避免热重载反复变化。
func TestEnsureWebPasswordBootstrapReusesStored(t *testing.T) {
	setupBootstrapConfig(t, "") // 配置文件不存在：写回静默失败
	a := newDefaultWebConfig()
	if res := EnsureWebPasswordBootstrap(a); res == nil {
		t.Fatal("首次应生成凭据")
	}
	firstUser, firstPw, firstPath := a.Web.Username, a.Web.Password, a.Web.Path

	b := newDefaultWebConfig()
	EnsureWebPasswordBootstrap(b)
	if b.Web.Username != firstUser || b.Web.Password != firstPw || b.Web.Path != firstPath {
		t.Fatalf("应复用已生成凭据 (%s/%s/%s)，实际 (%s/%s/%s)",
			firstUser, firstPw, firstPath, b.Web.Username, b.Web.Password, b.Web.Path)
	}
}

// withAndroidRuntime 在测试期间模拟 Android 运行环境。
func withAndroidRuntime(t *testing.T) {
	t.Helper()
	old := isAndroidRuntime
	isAndroidRuntime = true
	t.Cleanup(func() { isAndroidRuntime = old })
}

// Android 全新安装：不做随机化，保持公开默认值（三方 APK 无凭据展示界面也能登录）。
func TestEnsureWebPasswordBootstrapAndroidKeepsDefaults(t *testing.T) {
	withAndroidRuntime(t)
	setupBootstrapConfig(t, "web:\n    enabled: true\n    username: admin\n    password: admin\n    path: /web/\n")

	c := newDefaultWebConfig()
	if res := EnsureWebPasswordBootstrap(c); res != nil {
		t.Fatalf("Android 上不应随机化，实际 %+v", res)
	}
	if c.Web.Username != "admin" || c.Web.Password != "admin" || c.Web.Path != "/web/" {
		t.Fatalf("配置不应被修改: %+v", c.Web)
	}
	if InitialPasswordFileExists() {
		t.Fatal("Android 上不应生成凭据文件")
	}
}

// Android 旧安装（已随机化且用户从未登录）：回退默认值并写回配置、删除凭据文件。
func TestEnsureWebPasswordBootstrapAndroidRestoresRandomized(t *testing.T) {
	withAndroidRuntime(t)
	path := setupBootstrapConfig(t, "web:\n    enabled: true\n    username: admabc123456\n    password: \"R4nd0m-Pass-2026\"\n    path: /p8k2m4x9qz7c/\n")
	_ = os.WriteFile(InitialPasswordFile(), []byte("username: admabc123456\npassword: R4nd0m-Pass-2026\npath: /p8k2m4x9qz7c/\n"), 0o600)

	c := &Config{}
	c.Web.Enabled = true
	c.Web.Username = "admabc123456"
	c.Web.Password = "R4nd0m-Pass-2026"
	c.Web.Path = "/p8k2m4x9qz7c/"

	res := EnsureWebPasswordBootstrap(c)
	if res == nil || !res.AndroidDefault {
		t.Fatalf("应回退为 Android 默认凭据，实际 %+v", res)
	}
	if c.Web.Username != "admin" || c.Web.Password != "admin" || c.Web.Path != "/web/" {
		t.Fatalf("应回退默认值: %+v", c.Web)
	}
	doc := readPersistedWeb(t, path)
	if doc.Username != "admin" || doc.Password != "admin" || doc.Path != "/web/" {
		t.Fatalf("配置未正确写回: %+v", doc)
	}
	if InitialPasswordFileExists() {
		t.Fatal("回退成功后应删除凭据文件")
	}
	if !strings.Contains(res.Banner(), "Android") {
		t.Errorf("Banner 应说明 Android 默认凭据: %s", res.Banner())
	}
}

// Android 用户已改密（配置口令与凭据文件不一致）：完全不动，只清理残留文件。
func TestEnsureWebPasswordBootstrapAndroidKeepsCustomPassword(t *testing.T) {
	withAndroidRuntime(t)
	path := setupBootstrapConfig(t, "web:\n    enabled: true\n    username: admin\n    password: \"My-Str0ng-Pass\"\n    path: /web/\n")
	_ = os.WriteFile(InitialPasswordFile(), []byte("username: admabc123456\npassword: R4nd0m-Pass-2026\npath: /p8k2m4x9qz7c/\n"), 0o600)

	c := &Config{}
	c.Web.Enabled = true
	c.Web.Username = "admin"
	c.Web.Password = "My-Str0ng-Pass"
	c.Web.Path = "/web/"

	if res := EnsureWebPasswordBootstrap(c); res != nil {
		t.Fatalf("已改密不应改动，实际 %+v", res)
	}
	if c.Web.Password != "My-Str0ng-Pass" {
		t.Fatalf("口令被意外修改: %q", c.Web.Password)
	}
	doc := readPersistedWeb(t, path)
	if doc.Password != "My-Str0ng-Pass" {
		t.Fatalf("配置文件被意外修改: %+v", doc)
	}
	if InitialPasswordFileExists() {
		t.Fatal("已改密时残留凭据文件应被清理")
	}
}

// Android 改过用户名/路径（说明用户能登进后台）：即使口令还是初始随机值也不回退。
func TestEnsureWebPasswordBootstrapAndroidKeepsTouchedIdentity(t *testing.T) {
	withAndroidRuntime(t)
	path := setupBootstrapConfig(t, "web:\n    enabled: true\n    username: my-admin\n    password: \"R4nd0m-Pass-2026\"\n    path: /p8k2m4x9qz7c/\n")
	_ = os.WriteFile(InitialPasswordFile(), []byte("username: admabc123456\npassword: R4nd0m-Pass-2026\npath: /p8k2m4x9qz7c/\n"), 0o600)

	c := &Config{}
	c.Web.Enabled = true
	c.Web.Username = "my-admin"
	c.Web.Password = "R4nd0m-Pass-2026"
	c.Web.Path = "/p8k2m4x9qz7c/"

	if res := EnsureWebPasswordBootstrap(c); res != nil {
		t.Fatalf("身份被改过时不应回退，实际 %+v", res)
	}
	if c.Web.Username != "my-admin" || c.Web.Password != "R4nd0m-Pass-2026" {
		t.Fatalf("配置被意外修改: %+v", c.Web)
	}
	doc := readPersistedWeb(t, path)
	if doc.Username != "my-admin" || doc.Password != "R4nd0m-Pass-2026" {
		t.Fatalf("配置文件被意外修改: %+v", doc)
	}
}

func TestNeedsSecurityBootstrap(t *testing.T) {
	cases := []struct {
		p    string
		want bool
	}{
		{"admin", true},
		{"", true},
		{"Str0ng-Pass!2026", false},
	}
	for _, c := range cases {
		if got := needsSecurityBootstrap(c.p); got != c.want {
			t.Errorf("needsSecurityBootstrap(%q) = %v, want %v", c.p, got, c.want)
		}
	}
}
