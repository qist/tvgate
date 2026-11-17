package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// docRootDenySubtree 禁止作为 PHP docroot 的目录**及其所有子目录**。
var docRootDenySubtree = []string{
	// Linux / Unix：系统与内核相关
	"/etc", "/usr", "/bin", "/sbin", "/lib", "/lib32", "/lib64", "/libx32",
	"/boot", "/dev", "/proc", "/sys", "/run", "/root",
	"/var/lib", "/var/run", "/var/spool", "/var/tmp",
	// macOS
	"/system", "/library", "/private", "/applications", "/cores",
	// Windows（按去掉盘符后的路径匹配）
	"/windows", "/program files", "/program files (x86)", "/programdata",
	"/system volume information", "/$recycle.bin", "/recovery", "/perflogs",
}

// docRootDenyExact 只禁止目录本身，其下的业务子目录仍允许
// （例如 /opt/tvgate/www、/home/user/www、/www 都是常见部署位置）。
var docRootDenyExact = []string{
	"/home", "/opt", "/var", "/srv", "/mnt", "/media", "/data", "/tmp", "/lost+found",
}

// firstHiddenSegment 返回路径中第一个以 . 开头的目录段（.ssh / .gnupg / .git / .config 等）。
// 这些目录通常存放私钥、凭据或版本库，交给 PHP 脚本与代码管理页等于全部泄露/可被删除。
func firstHiddenSegment(p string) string {
	for _, seg := range strings.Split(filepath.ToSlash(p), "/") {
		if len(seg) > 1 && strings.HasPrefix(seg, ".") && seg != ".." {
			return seg
		}
	}
	return ""
}

// isUserHomeDir 判断是否为用户家目录本身（/home/qist、/Users/qist、C:\Users\qist）。
// 家目录下的业务子目录（如 /home/qist/www）不受影响。
func isUserHomeDir(p string) bool {
	segs := strings.Split(strings.Trim(filepath.ToSlash(p), "/"), "/")
	if len(segs) > 0 && strings.HasSuffix(segs[0], ":") { // Windows 盘符
		segs = segs[1:]
	}
	if len(segs) != 2 {
		return false
	}
	switch strings.ToLower(segs[0]) {
	case "home", "users", "documents and settings":
		return true
	}
	return false
}

// ResolveDocRoot 把配置里的 docroot 原值解析为绝对路径：
// 支持 ~ / ~/ 家目录写法，相对路径以配置文件所在目录为基准（与 SetDefaults 保持一致）。
func ResolveDocRoot(raw string) string {
	p := expandHome(strings.TrimSpace(raw))
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) {
		if ConfigFilePath != nil && *ConfigFilePath != "" {
			p = filepath.Join(filepath.Dir(expandHome(*ConfigFilePath)), p)
		}
	}
	return filepath.Clean(p)
}

// ValidateDocRoot 校验 docroot 是否可作为 PHP 脚本根目录。
//
// 拒绝以下情况（防止把 / 或系统目录设为 docroot 后，通过 PHP 脚本的
// 绝对路径文件操作、后台代码管理页的删除功能破坏系统文件）：
//   - 空路径、磁盘根目录（/、C:\）
//   - 系统/敏感目录及其子目录（/etc、/usr、/proc、/root、C:\Windows 等）
//   - 顶层公共目录本身（/home、/opt、/var、/tmp 等，允许其下的业务子目录）
//   - 配置文件或程序本体所在的目录及其祖先目录（允许其下的子目录，如 www）
//
// 例外：位于「配置文件所在目录」下的子目录始终允许（如 Docker 默认的
// /etc/tvgate/www），否则标准容器部署会被误杀。
//
// 会同时检查路径本身与符号链接解析后的真实路径，防止用软链接绕过。
func ValidateDocRoot(raw string) error {
	p := ResolveDocRoot(raw)
	if p == "" {
		return fmt.Errorf("docroot 不能为空")
	}
	if err := checkDocRootPath(p); err != nil {
		return err
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		if real != p {
			if err := checkDocRootPath(real); err != nil {
				return fmt.Errorf("%v（符号链接实际指向 %s）", err, real)
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("无法解析 docroot 路径: %v", err)
	}
	return nil
}

// configDir 返回配置文件所在目录的绝对路径（无配置文件时返回空串）。
func configDir() string {
	if ConfigFilePath == nil || *ConfigFilePath == "" {
		return ""
	}
	abs, err := filepath.Abs(*ConfigFilePath)
	if err != nil {
		return ""
	}
	dir := filepath.Dir(abs)
	// 配置就在磁盘根目录时不做任何例外，避免把根目录当成「配置目录」放行一切
	if dir == filepath.VolumeName(dir)+string(os.PathSeparator) {
		return ""
	}
	return dir
}

// underConfigDir 判断 p 是否为配置文件所在目录的严格子目录（不含目录本身）。
func underConfigDir(p, cfgDir string) bool {
	if cfgDir == "" {
		return false
	}
	rel, err := filepath.Rel(cfgDir, p)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// checkDocRootPath 对单个（已解析的）路径做安全校验。
func checkDocRootPath(p string) error {
	clean := filepath.Clean(p)
	vol := filepath.VolumeName(clean)
	root := vol + string(os.PathSeparator)
	if clean == root || clean == vol || strings.Trim(clean, `/\`) == "" {
		return fmt.Errorf("docroot 不能是磁盘根目录: %s", p)
	}

	// 统一成小写、正斜杠，并分别保留「含盘符」和「去掉盘符」两种形式，
	// 以同时匹配 Linux 绝对路径与 Windows 盘符路径。
	lower := strings.ToLower(filepath.ToSlash(clean))
	noVol := lower
	if vol != "" {
		noVol = strings.TrimPrefix(lower, strings.ToLower(filepath.ToSlash(vol)))
	}
	match := func(d string) bool {
		return lower == d || noVol == d
	}
	subtree := func(d string) bool {
		return lower == d || strings.HasPrefix(lower, d+"/") ||
			noVol == d || strings.HasPrefix(noVol, d+"/")
	}

	// 配置文件所在目录下的子目录始终放行（Docker 默认 /etc/tvgate/www 属于此类）
	cfgDir := configDir()
	exempt := underConfigDir(clean, cfgDir)

	// 用户家目录本身不能作为 docroot：里面是 .ssh/.gnupg/.config/浏览器数据，
	// 等于把私钥与全部用户配置交给 PHP 脚本和代码管理页。
	if isUserHomeDir(clean) {
		return fmt.Errorf("docroot 不能是用户家目录本身（请使用其下的业务子目录，如 ~/www）: %s", p)
	}

	// 隐藏目录（.ssh、.gnupg、.git、.config 等）不能作为 docroot。
	// 例外：配置文件所在目录自身含隐藏段（如 ~/.config/tvgate）时，只检查其下的相对路径。
	hiddenScan := clean
	if exempt {
		if rel, err := filepath.Rel(cfgDir, clean); err == nil {
			hiddenScan = rel
		}
	}
	if seg := firstHiddenSegment(hiddenScan); seg != "" {
		return fmt.Errorf("docroot 不能包含隐藏目录 %q（如 .ssh/.gnupg/.git 常存放私钥或凭据）: %s", seg, p)
	}

	if !exempt {
		for _, d := range docRootDenySubtree {
			if subtree(d) {
				return fmt.Errorf("docroot 不能位于系统/敏感目录 %s: %s", d, p)
			}
		}
		for _, d := range docRootDenyExact {
			if match(d) {
				return fmt.Errorf("docroot 不能是 %s 本身（请使用其下的业务子目录）: %s", d, p)
			}
		}
		if runtime.GOOS == "windows" && (match("/users") || match("/documents and settings")) {
			return fmt.Errorf("docroot 不能是用户根目录本身（请使用其下的业务子目录）: %s", p)
		}
	}

	// 不允许 docroot 覆盖配置文件或程序本体所在目录，避免代码管理页误删关键文件。
	// 注意此处不因 exempt 放宽：docroot 必须是配置目录的子目录，而不是其本身/父目录。
	var protected []string
	if ConfigFilePath != nil && *ConfigFilePath != "" {
		protected = append(protected, *ConfigFilePath)
	}
	if exe, err := os.Executable(); err == nil {
		protected = append(protected, exe)
	}
	for _, f := range protected {
		if f == "" {
			continue
		}
		abs, err := filepath.Abs(f)
		if err != nil {
			continue
		}
		if isSameOrAncestorDir(clean, abs) {
			return fmt.Errorf("docroot 不能包含关键文件 %s（请使用其下的子目录，如 www）: %s", abs, p)
		}
	}
	return nil
}

// isSameOrAncestorDir 判断 dir 是否等于 file 的所在目录或为其祖先目录。
func isSameOrAncestorDir(dir, file string) bool {
	rel, err := filepath.Rel(dir, file)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}
