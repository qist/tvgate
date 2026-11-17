package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestValidateDocRoot(t *testing.T) {
	dir := t.TempDir()
	old := *ConfigFilePath
	*ConfigFilePath = filepath.Join(dir, "config.yaml")
	t.Cleanup(func() { *ConfigFilePath = old })

	bad := []string{
		"",
		"/",
		"/etc",
		"/etc/nginx",
		"/usr/local/www",
		"/proc/1",
		"/sys/kernel",
		"/root",
		"/root/www",
		"/opt",       // 顶层目录本身
		"/var",       // 顶层目录本身
		"/tmp",       // 顶层目录本身
		"/var/lib/x", // 系统数据目录
		dir,          // 配置文件所在目录本身
	}
	for _, p := range bad {
		if err := ValidateDocRoot(p); err == nil {
			t.Errorf("ValidateDocRoot(%q) 应为不安全，实际通过", p)
		}
	}

	good := []string{
		filepath.Join(dir, "www"),
		filepath.Join(dir, "a", "b"),
		"/var/www",
		"/www",
		"/srv/tvgate/www",
		"/opt/tvgate/www",
		"/home/user/www",
	}
	for _, p := range good {
		if err := ValidateDocRoot(p); err != nil {
			t.Errorf("ValidateDocRoot(%q) 应为安全，实际拒绝: %v", p, err)
		}
	}
}

// Docker 默认部署：配置文件在 /etc/tvgate/config.yaml、docroot 默认 www，
// 解析后为 /etc/tvgate/www，必须放行（否则标准容器部署会被误杀）。
func TestValidateDocRootDockerDefault(t *testing.T) {
	old := *ConfigFilePath
	*ConfigFilePath = "/etc/tvgate/config.yaml"
	t.Cleanup(func() { *ConfigFilePath = old })

	if err := ValidateDocRoot("/etc/tvgate/www"); err != nil {
		t.Fatalf("/etc/tvgate/www 应放行，实际拒绝: %v", err)
	}
	for _, p := range []string{"/etc/tvgate", "/etc/tvgate/config.yaml", "/etc/nginx"} {
		if err := ValidateDocRoot(p); err == nil {
			t.Errorf("ValidateDocRoot(%q) 应拒绝，实际通过", p)
		}
	}
}

// 家目录本身与隐藏目录（.ssh 等）不能作为 docroot：
// 否则 PHP 脚本 / 代码管理页可读写删除私钥、凭据与版本库。
func TestValidateDocRootRejectsHomeAndHidden(t *testing.T) {
	dir := t.TempDir()
	old := *ConfigFilePath
	*ConfigFilePath = filepath.Join(dir, "config.yaml")
	t.Cleanup(func() { *ConfigFilePath = old })

	bad := []string{
		"/home/qist/.ssh",
		"/home/qist/.ssh/keys",
		"/home/qist",           // 家目录本身
		"/Users/qist",          // macOS 家目录本身
		"/Users/qist/.ssh",     // macOS 隐藏目录
		"/srv/app/.gnupg",      // 隐藏目录
		"/opt/tvgate/www/.git", // 版本库/隐藏目录
		"/var/www/.config",
	}
	for _, p := range bad {
		if err := ValidateDocRoot(p); err == nil {
			t.Errorf("ValidateDocRoot(%q) 应拒绝，实际通过", p)
		}
	}

	good := []string{
		"/home/qist/www",   // 家目录下的业务子目录
		"/Users/qist/site", // macOS 家目录下的业务子目录
		"/srv/app/www",     // 普通业务目录
		"/var/www/html",    // 常见站点目录
		"/opt/tvgate/build/www",
	}
	for _, p := range good {
		if err := ValidateDocRoot(p); err != nil {
			t.Errorf("ValidateDocRoot(%q) 应放行，实际拒绝: %v", p, err)
		}
	}
}

// 配置文件自身在隐藏目录（如 ~/.config/tvgate）时，其下子目录仍可用作 docroot。
func TestValidateDocRootHiddenConfigDirExempt(t *testing.T) {
	old := *ConfigFilePath
	*ConfigFilePath = "/home/qist/.config/tvgate/config.yaml"
	t.Cleanup(func() { *ConfigFilePath = old })

	if err := ValidateDocRoot("/home/qist/.config/tvgate/www"); err != nil {
		t.Fatalf("配置目录下的 www 应放行，实际拒绝: %v", err)
	}
	// 但不能借此把整个 .config 或家目录设成 docroot
	for _, p := range []string{"/home/qist/.config", "/home/qist"} {
		if err := ValidateDocRoot(p); err == nil {
			t.Errorf("ValidateDocRoot(%q) 应拒绝，实际通过", p)
		}
	}
}

func TestValidateDocRootSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows 需要管理员权限创建符号链接")
	}
	dir := t.TempDir()
	old := *ConfigFilePath
	*ConfigFilePath = filepath.Join(dir, "config.yaml")
	t.Cleanup(func() { *ConfigFilePath = old })

	link := filepath.Join(dir, "www")
	if err := os.Symlink("/etc", link); err != nil {
		t.Skipf("无法创建符号链接: %v", err)
	}
	if err := ValidateDocRoot(link); err == nil {
		t.Fatalf("指向 /etc 的软链接应被拒绝")
	}
}
