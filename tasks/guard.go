package tasks

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/qist/tvgate/config"
)

// 定时任务命令安全拦截。
//
// 说明：定时任务本身等价于 crontab，具备面板运行用户权限。这里做的是
// 「兜底黑名单」——只拦明显会造成系统级破坏的操作（格式化磁盘、删根、
// 覆写块设备、关机、fork 炸弹、下载即执行等），不是沙箱，无法穷尽所有
// 绕过方式（混淆、编译脚本等）。真正的权限边界仍是面板运行用户与后台访问控制。
// 需要彻底隔离时应使用容器/低权限用户运行。

// blockedPrograms 直接禁止执行的程序（按 basename 匹配），value 为拦截原因。
var blockedPrograms = map[string]string{
	"mkfs":     "格式化文件系统",
	"mke2fs":   "格式化文件系统",
	"mkdosfs":  "格式化文件系统",
	"mkswap":   "创建交换分区",
	"wipefs":   "擦除文件系统签名",
	"fdisk":    "修改磁盘分区表",
	"sfdisk":   "修改磁盘分区表",
	"parted":   "修改磁盘分区表",
	"sgdisk":   "修改磁盘分区表",
	"gdisk":    "修改磁盘分区表",
	"cfdisk":   "修改磁盘分区表",
	"shutdown": "关机/重启系统",
	"reboot":   "关机/重启系统",
	"halt":     "关机/重启系统",
	"poweroff": "关机/重启系统",
	"kexec":    "关机/重启系统",
	"killall5": "终止系统全部进程",
}

// protectedTreeDirs 命中即视为系统关键路径（含子目录），禁止被递归删除/改权限/覆盖。
var protectedTreeDirs = []string{
	"/etc", "/usr", "/bin", "/sbin", "/lib", "/lib32", "/lib64", "/libx32",
	"/boot", "/dev", "/proc", "/sys", "/run", "/root",
	"/var/lib", "/var/run", "/var/spool", "/var/tmp",
	"/system", "/library", "/private", "/applications", "/cores",
	"/windows", "/program files", "/program files (x86)", "/programdata",
	"/system volume information", "/$recycle.bin", "/recovery", "/perflogs",
}

// protectedExactDirs 只拦目录本身（其下业务子目录允许操作，如 /opt/tvgate/www）。
var protectedExactDirs = []string{
	"/", "/home", "/opt", "/var", "/srv", "/mnt", "/media", "/data", "/tmp",
	"/users", "/documents and settings",
}

// dangerousPatterns 针对整条命令的危险模式（大小写不敏感）。
var dangerousPatterns = []struct {
	re     *regexp.Regexp
	reason string
}{
	{regexp.MustCompile(`\brm\s+[^;&|]*--no-preserve-root`), "递归删除根目录"},
	{regexp.MustCompile(`:\s*\(\s*\)\s*\{[^}]*:\s*\|\s*:`), "fork 炸弹"},
	{regexp.MustCompile(`(^|[^0-9a-z])>\s*/dev/(sd|hd|nvme|vd|mmcblk|xvd)`), "覆写磁盘设备"},
	{regexp.MustCompile(`\bdd\b[^;&|]*\bof=/dev/(sd|hd|nvme|vd|mmcblk|xvd)`), "覆写磁盘设备"},
	{regexp.MustCompile(`\b(curl|wget)\b[^;&|]*\|\s*(sudo\s+)?(ba|da|z|k|c)?sh\b`), "从网络下载并直接执行脚本"},
	{regexp.MustCompile(`\bfind\s+/\s[^;&|]*-delete`), "递归删除根目录文件"},
	{regexp.MustCompile(`\bfind\s+/\s[^;&|]*-exec\s+rm\b`), "递归删除根目录文件"},
	{regexp.MustCompile(`(^|\s)>\s*/(etc|bin|sbin|usr|boot|lib)/`), "覆写系统文件"},
}

// docRootHolder 记录当前 PHP docroot（由 tasks.Start 填充）。
// 用 atomic.Value 避免与配置热加载写并发；整个 docroot 不允许被任务直接删除。
var docRootHolder atomic.Value

// SetDocRoot 记录当前 PHP 脚本根目录，供危险命令判定使用。
func SetDocRoot(p string) {
	docRootHolder.Store(strings.TrimSpace(p))
}

func configuredDocRoot() string {
	v, _ := docRootHolder.Load().(string)
	return v
}

// ValidateCommand 校验定时任务命令，命中危险操作时返回原因。
// php:// 命令走内嵌 phpgo 解释器、不经过系统 shell，直接放行。
func ValidateCommand(command string) error {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return nil
	}
	if strings.HasPrefix(cmd, "php://") {
		return nil
	}

	lower := strings.ToLower(cmd)
	for _, p := range dangerousPatterns {
		if p.re.MatchString(lower) {
			return fmt.Errorf("禁止执行危险命令（%s）: %s", p.reason, abbreviate(cmd))
		}
	}

	for _, seg := range splitShellSegments(cmd) {
		fields := unwrapShellPrefix(shellFields(seg))
		if len(fields) == 0 {
			continue
		}
		if err := checkSegment(fields); err != nil {
			return err
		}
	}
	return nil
}

// checkSegment 检查单个简单命令（已去除 sudo/env/timeout 等包装）。
func checkSegment(fields []string) error {
	prog := strings.ToLower(filepath.Base(fields[0]))
	prog = strings.TrimSuffix(prog, ".exe")
	args := fields[1:]

	if reason, ok := blockedPrograms[prog]; ok {
		return fmt.Errorf("禁止执行危险命令（%s）: %s", reason, abbreviate(strings.Join(fields, " ")))
	}
	if strings.HasPrefix(prog, "mkfs.") {
		return fmt.Errorf("禁止执行危险命令（格式化文件系统）: %s", abbreviate(strings.Join(fields, " ")))
	}

	// 解释器包装：继续检查内层命令，避免 sh -c 'rm -rf /' 这类绕过
	switch prog {
	case "sh", "bash", "dash", "zsh", "ksh", "ash":
		if inner := flagValue(args, "-c", "--command"); inner != "" {
			return ValidateCommand(inner)
		}
		// 组合短参数（-ec / -xc 等）里的 c 同样代表 -c
		for i, a := range args {
			if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "c") && i+1 < len(args) {
				return ValidateCommand(strings.Join(args[i+1:], " "))
			}
		}
	case "cmd":
		if inner := flagValue(args, "/c", "/k"); inner != "" {
			return ValidateCommand(inner)
		}
	case "powershell", "pwsh":
		if inner := flagValue(args, "-command", "-c", "-encodedcommand", "-e"); inner != "" {
			// -EncodedCommand 是 base64 载荷，无法审计，直接拒绝
			for _, a := range args {
				if strings.EqualFold(a, "-encodedcommand") || strings.EqualFold(a, "-e") {
					return fmt.Errorf("禁止执行危险命令（编码后的 PowerShell 载荷无法审计）: %s", abbreviate(strings.Join(fields, " ")))
				}
			}
			return ValidateCommand(inner)
		}
	case "eval":
		if len(args) > 0 {
			return ValidateCommand(strings.Join(args, " "))
		}
	}

	switch prog {
	case "rm":
		if isRecursiveFlag(args) && hasProtectedTarget(args) {
			return fmt.Errorf("禁止执行危险命令（递归删除系统/根目录）: %s", abbreviate(strings.Join(fields, " ")))
		}
	case "chmod", "chown", "chgrp":
		if isRecursiveFlag(args) && hasProtectedTarget(args) {
			return fmt.Errorf("禁止执行危险命令（递归修改系统/根目录权限）: %s", abbreviate(strings.Join(fields, " ")))
		}
	case "mv":
		if hasProtectedTarget(args) {
			return fmt.Errorf("禁止执行危险命令（移动系统/根目录）: %s", abbreviate(strings.Join(fields, " ")))
		}
	case "cp", "install", "rsync":
		if dest := lastNonFlagArg(args); dest != "" && isProtectedTarget(dest) {
			return fmt.Errorf("禁止执行危险命令（覆盖系统/根目录）: %s", abbreviate(strings.Join(fields, " ")))
		}
	case "kill":
		// kill -9 1 / kill 1：直接杀 PID 1
		for _, a := range args {
			if a == "1" {
				return fmt.Errorf("禁止执行危险命令（终止 PID 1 会拖垮系统）: %s", abbreviate(strings.Join(fields, " ")))
			}
		}
	}
	return nil
}

// splitShellSegments 按 shell 控制符拆分命令（忽略引号内的分隔符）。
func splitShellSegments(s string) []string {
	var (
		segs  []string
		b     strings.Builder
		quote rune
	)
	flush := func() {
		if seg := strings.TrimSpace(b.String()); seg != "" {
			segs = append(segs, seg)
		}
		b.Reset()
	}
	for _, r := range s {
		if quote != 0 {
			b.WriteRune(r)
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
			b.WriteRune(r)
		case ';', '\n', '&', '|':
			flush()
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return segs
}

// shellFields 按空白拆分参数，支持单/双引号（保留引号之间的整体）。
func shellFields(s string) []string {
	var (
		out   []string
		b     strings.Builder
		quote rune
		has   bool
	)
	flush := func() {
		if has {
			out = append(out, b.String())
			b.Reset()
			has = false
		}
	}
	for _, r := range s {
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
			has = true
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
			has = true
		case ' ', '\t', '\r':
			flush()
		default:
			b.WriteRune(r)
			has = true
		}
	}
	flush()
	return out
}

// unwrapShellPrefix 去掉 sudo/env/nohup/nice/timeout/xargs 等包装，返回真正的程序及其参数。
func unwrapShellPrefix(fields []string) []string {
	for len(fields) > 0 {
		base := strings.ToLower(filepath.Base(fields[0]))
		switch base {
		case "sudo", "doas":
			fields = fields[1:]
			for len(fields) > 0 && strings.HasPrefix(fields[0], "-") {
				if (fields[0] == "-u" || fields[0] == "-g" || fields[0] == "-p") && len(fields) > 1 {
					fields = fields[2:]
					continue
				}
				fields = fields[1:]
			}
		case "env":
			fields = fields[1:]
			for len(fields) > 0 && (strings.Contains(fields[0], "=") || strings.HasPrefix(fields[0], "-")) {
				fields = fields[1:]
			}
		case "nohup", "setsid", "command", "exec", "stdbuf", "time", "busybox":
			fields = fields[1:]
			for len(fields) > 0 && strings.HasPrefix(fields[0], "-") {
				fields = fields[1:]
			}
		case "nice", "ionice":
			fields = fields[1:]
			for len(fields) > 0 && strings.HasPrefix(fields[0], "-") {
				fields = fields[1:]
			}
			if len(fields) > 0 && isDigits(fields[0]) {
				fields = fields[1:]
			}
		case "timeout":
			fields = fields[1:]
			for len(fields) > 0 && strings.HasPrefix(fields[0], "-") {
				fields = fields[1:]
			}
			if len(fields) > 0 {
				fields = fields[1:] // 时长
			}
		case "xargs":
			fields = fields[1:]
			for len(fields) > 0 && strings.HasPrefix(fields[0], "-") {
				fields = fields[1:]
			}
		default:
			return fields
		}
	}
	return fields
}

// flagValue 返回 -c/--command 类参数后的值（取剩余参数拼接，便于检查内层命令）。
func flagValue(args []string, flags ...string) string {
	for i, a := range args {
		for _, f := range flags {
			if strings.EqualFold(a, f) && i+1 < len(args) {
				return strings.Join(args[i+1:], " ")
			}
			// -c"cmd" / /c"cmd" 紧贴写法
			if len(a) > len(f) && strings.EqualFold(a[:len(f)], f) {
				return a[len(f):]
			}
		}
	}
	return ""
}

// isRecursiveFlag 判断是否带递归参数（-r/-R/--recursive 或组合短参数）。
func isRecursiveFlag(args []string) bool {
	for _, a := range args {
		if a == "--" {
			break
		}
		if !strings.HasPrefix(a, "-") {
			continue
		}
		if a == "--recursive" {
			return true
		}
		if !strings.HasPrefix(a, "--") && (strings.Contains(a, "r") || strings.Contains(a, "R")) {
			return true
		}
	}
	return false
}

// hasProtectedTarget 判断参数里是否出现系统关键路径（含程序本体/配置文件祖先目录）。
func hasProtectedTarget(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if isProtectedTarget(a) {
			return true
		}
	}
	return false
}

// lastNonFlagArg 返回最后一个非选项参数（用于 cp/rsync 的目标位置）。
func lastNonFlagArg(args []string) string {
	last := ""
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		last = a
	}
	return last
}

// isProtectedTarget 判断目标是否为根目录、系统关键目录、或程序/配置文件的祖先目录。
func isProtectedTarget(p string) bool {
	p = strings.TrimSpace(strings.Trim(p, `"'`))
	if p == "" {
		return false
	}
	lower := strings.ToLower(p)
	switch lower {
	case "/", "/*", "~", "~/", "$home", "${home}", "%userprofile%", "c:\\", "c:/":
		return true
	}
	if strings.ContainsAny(lower, "$`") {
		// 变量/命令替换后的目标无法静态判断，按危险处理（避免 rm -rf $HOME 之类）
		if strings.Contains(lower, "home") || strings.Contains(lower, "root") {
			return true
		}
	}

	clean := filepath.Clean(p)
	if !filepath.IsAbs(clean) {
		return false
	}
	if clean == filepath.VolumeName(clean)+string(os.PathSeparator) {
		return true
	}
	slashed := strings.ToLower(filepath.ToSlash(clean))
	for _, d := range protectedTreeDirs {
		if slashed == d || strings.HasPrefix(slashed, d+"/") {
			return true
		}
	}
	for _, d := range protectedExactDirs {
		if slashed == d {
			return true
		}
	}

	// docroot 整目录不允许被任务直接删除/覆盖（其子目录仍允许，如清理缓存）
	if dr := configuredDocRoot(); dr != "" {
		if drAbs, err := filepath.Abs(dr); err == nil {
			if filepath.Clean(drAbs) == filepath.Clean(p) {
				return true
			}
		}
	}

	// 动态保护：程序本体、配置文件所在目录及其祖先，不能被任务删除/覆盖
	protected := make([]string, 0, 2)
	if config.ConfigFilePath != nil && *config.ConfigFilePath != "" {
		protected = append(protected, *config.ConfigFilePath)
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
		rel, err := filepath.Rel(clean, abs)
		if err != nil {
			continue
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))) {
			return true
		}
	}
	return false
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	_, err := strconv.Atoi(s)
	return err == nil
}

func abbreviate(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}
