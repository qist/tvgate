package config

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// 后台安全引导：不再使用公开默认值（用户名 admin / 口令 admin / 路径 /web/）。
//
// 公开默认值的问题：口令是公开信息，谁先登进去改密谁就是管理员；用户名与路径
// 同样是公开的，扫描器一眼就能找到入口。因此每次加载配置时（启动 + 热重载）检查：
//   - **只有口令仍是空或默认值 admin 时**才做初始化（口令已自定义则什么都不改，
//     用户名、路径都保持用户设置）；
//   - 初始化时，用户名/路径也只在本身仍是默认值时才替换为随机值；
//   - 新值写回配置文件（保留注释），并写入 <配置目录>/web-initial-password.txt（0600），
//     同时打印到控制台与日志，便于取用；
//   - 三项都不会再次触发（口令已是随机强口令），热重载不会反复变化；
//   - 用户改密成功后，口令文件自动删除。
//
// Android 例外：TVGate 常以 .so 形式内嵌在三方 APK 中，宿主 App 未必有展示
// web-initial-password.txt 的界面，随机化会让用户永远登不进后台；因此
// GOOS=android（APK 内嵌或独立二进制）不做随机化，保持公开默认值，由「弱口令
// 锁写」兜底（不改密则写配置/代码/任务等高风险操作一律被拒绝）。
//
// 也支持用环境变量 TVGATE_ADMIN_PASSWORD 指定口令（不再随机）。
const (
	// EnvWebAdminPassword 显式指定后台初始口令的环境变量
	EnvWebAdminPassword = "TVGATE_ADMIN_PASSWORD"
	// initialPasswordFileName 初始凭据文件（与配置文件同目录）
	initialPasswordFileName = "web-initial-password.txt"
	// defaultWebPassword 内嵌默认口令（公开信息，必须替换）
	defaultWebPassword = "admin"
	// defaultWebUsername 内嵌默认用户名（公开信息，必须替换）
	defaultWebUsername = "admin"
	// defaultWebPath 内嵌默认后台路径（公开信息，必须替换）
	defaultWebPath = "/web/"
	// initialPasswordLength 自动生成口令长度
	initialPasswordLength = 16
	// initialUsernameLength 自动生成用户名长度
	initialUsernameLength = 10
	// initialPathTokenLength 自动生成路径 token 长度
	initialPathTokenLength = 12
	// minEnvPasswordLength 环境变量口令的最小长度
	minEnvPasswordLength = 10
)

// 排除易混淆字符（0/O/o/1/l/I），并避免 YAML 特殊起始字符问题（写回时强制双引号）
const initialPasswordCharset = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789@#%*-_=+"

// 用户名 / 路径只用小写字母与数字，便于输入、且不会撞到 URL 特殊字符
const initialTokenCharset = "abcdefghijkmnpqrstuvwxyz23456789"

// isAndroidRuntime 标识当前进程是否运行在 Android（APK 内嵌 .so 或独立二进制）。
// 声明为变量便于单元测试在非 Android 环境下覆盖。
var isAndroidRuntime = runtime.GOOS == "android"

// InitialPasswordResult 描述本次启动对后台凭据的处理结果（nil 表示无需处理）。
type InitialPasswordResult struct {
	Username       string // 后台用户名
	Password       string // 后台口令
	Path           string // 后台访问路径
	File           string // 凭据文件路径（可能为空）
	Source         string // 来源说明（环境变量 / 自动生成 / 复用）
	AndroidDefault bool   // Android 回退到公开默认值（便于内嵌 APK 登录）
}

// InitialPasswordFile 返回初始凭据文件路径（未指定配置文件时返回空串）。
func InitialPasswordFile() string {
	if ConfigFilePath == nil || *ConfigFilePath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(*ConfigFilePath), initialPasswordFileName)
}

// InitialPasswordFileExists 判断初始凭据文件是否还在（用户尚未改密）。
func InitialPasswordFileExists() bool {
	p := InitialPasswordFile()
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

// ClearInitialPasswordFile 删除初始凭据文件（用户改密后调用）。
func ClearInitialPasswordFile() {
	if p := InitialPasswordFile(); p != "" {
		_ = os.Remove(p)
	}
}

// storedBootstrap 是凭据文件里记录的三项。
type storedBootstrap struct {
	Username string
	Password string
	Path     string
}

// readInitialPasswordFile 读取初始凭据文件。
func readInitialPasswordFile() storedBootstrap {
	var out storedBootstrap
	p := InitialPasswordFile()
	if p == "" {
		return out
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "username":
			out.Username = v
		case "password":
			out.Password = v
		case "path":
			out.Path = v
		}
	}
	return out
}

// randomFromCharset 用 crypto/rand 从字符集生成随机串。
func randomFromCharset(n int, charset string) (string, error) {
	if n <= 0 {
		return "", fmt.Errorf("长度必须大于 0")
	}
	out := make([]byte, n)
	max := big.NewInt(int64(len(charset)))
	for i := range out {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = charset[idx.Int64()]
	}
	return string(out), nil
}

// randomWebPassword 生成随机口令。
func randomWebPassword(n int) (string, error) {
	if n <= 0 {
		n = initialPasswordLength
	}
	return randomFromCharset(n, initialPasswordCharset)
}

// randomWebUsername 生成随机用户名（不以 admin 开头，避免被字典猜到）。
func randomWebUsername() (string, error) {
	tok, err := randomFromCharset(initialUsernameLength, initialTokenCharset)
	if err != nil {
		return "", err
	}
	return "adm" + tok, nil
}

// randomWebPath 生成随机后台路径，如 /p8k2m4x9qz7c/。
func randomWebPath() (string, error) {
	tok, err := randomFromCharset(initialPathTokenLength, initialTokenCharset)
	if err != nil {
		return "", err
	}
	return "/" + tok + "/", nil
}

// isDefaultWebUsername 判断用户名是否仍是公开默认值。
func isDefaultWebUsername(u string) bool {
	return strings.EqualFold(strings.TrimSpace(u), defaultWebUsername)
}

// isDefaultWebPath 判断后台路径是否仍是公开默认值（空、/web、/web/）。
func isDefaultWebPath(p string) bool {
	p = strings.Trim(strings.TrimSpace(p), "/")
	return p == "" || strings.EqualFold(p, strings.Trim(defaultWebPath, "/"))
}

// isDefaultWebPassword 判断口令是否为空或仍是公开默认值。
func isDefaultWebPassword(pw string) bool {
	pw = strings.TrimSpace(pw)
	return pw == "" || strings.EqualFold(pw, defaultWebPassword)
}

// needsSecurityBootstrap 仅当口令仍是默认值/空时才需要初始化。
// 口令已自定义时，用户名与路径保持用户设置，不做任何改动。
func needsSecurityBootstrap(password string) bool {
	return isDefaultWebPassword(password)
}

// EnsureWebPasswordBootstrap 在配置加载完成后调用（启动与热重载共用）。
// 返回非 nil 时应把结果打印到日志，提示管理员的取用方式。
func EnsureWebPasswordBootstrap(c *Config) *InitialPasswordResult {
	if c == nil || !c.Web.Enabled {
		return nil
	}

	// Android：不做随机化（三方 APK 无法向用户展示随机凭据），保持公开默认值。
	if isAndroidRuntime {
		return ensureAndroidDefaultCredentials(c)
	}

	if !needsSecurityBootstrap(c.Web.Password) {
		// 口令已经是自定义值：什么都不动（用户名 admin、路径 /web/ 也保持原样），
		// 只清理过期的初始凭据文件。
		if stored := readInitialPasswordFile(); stored.Password != "" && stored.Password != strings.TrimSpace(c.Web.Password) {
			ClearInitialPasswordFile()
		}
		return nil
	}

	res := &InitialPasswordResult{File: InitialPasswordFile()}

	// 1) 环境变量指定口令（口令是默认值时生效；用户名/路径仅在本身是默认值时才随机化）
	envPw := strings.TrimSpace(os.Getenv(EnvWebAdminPassword))
	if envPw != "" {
		if len([]rune(envPw)) < minEnvPasswordLength ||
			strings.EqualFold(envPw, defaultWebPassword) ||
			strings.EqualFold(envPw, strings.TrimSpace(c.Web.Username)) {
			envPw = "" // 过弱：忽略，回落到随机口令
			res.Source = fmt.Sprintf("环境变量 %s 口令过弱已忽略，改为自动生成", EnvWebAdminPassword)
		}
	}

	// 2) 上次已生成但配置回写失败（只读挂载等）：复用文件里的值，避免每次重载都变
	stored := readInitialPasswordFile()
	canReuse := len([]rune(stored.Password)) >= minEnvPasswordLength &&
		!strings.EqualFold(stored.Password, defaultWebPassword)

	if isDefaultWebUsername(c.Web.Username) {
		if canReuse {
			c.Web.Username = stored.Username
		} else if u, err := randomWebUsername(); err == nil {
			c.Web.Username = u
		}
	}
	if isDefaultWebPath(c.Web.Path) {
		if canReuse {
			c.Web.Path = stored.Path
		} else if p, err := randomWebPath(); err == nil {
			c.Web.Path = p
		}
	}
	// 口令：只要本次检测到任一项仍是默认值（即「未完成安全初始化」），就轮换口令。
	// 这样即使旧部署只改过口令、用户名/路径仍是 admin 与 /web/，升级后也会拿到一份
	// 全新凭据，避免旧口令曾在默认身份下暴露过。三项全部自定义后不再触发。
	switch {
	case envPw != "":
		c.Web.Password = envPw
		if res.Source == "" {
			res.Source = "环境变量 " + EnvWebAdminPassword
		}
	case canReuse:
		c.Web.Password = stored.Password
		if res.Source == "" {
			res.Source = "复用上次生成的随机凭据"
		}
	default:
		if pw, err := randomWebPassword(initialPasswordLength); err == nil {
			c.Web.Password = pw
		}
		if res.Source == "" {
			res.Source = "检测到默认值，已随机化"
		}
	}

	if res.Source == "" {
		res.Source = "检测到默认值，已随机化"
	}
	res.Username = strings.TrimSpace(c.Web.Username)
	res.Password = strings.TrimSpace(c.Web.Password)
	res.Path = normalizedWebPath(c.Web.Path)

	writeInitialPasswordFile(res.Username, res.Password, res.Path)
	persistWebBootstrap(res.Username, res.Password, res.Path)
	return res
}

// ensureAndroidDefaultCredentials 处理 Android 运行时的后台凭据。
//
// Android 上 TVGate 通常以 .so 形式内嵌在三方 APK 中，宿主 App 未必有展示
// web-initial-password.txt 的界面；一旦随机化，用户既不知道用户名也拿不到口令，
// 后台等于永久锁死。因此 Android 不做随机化，保持公开默认值（admin / admin /
// /web/），由「弱口令锁写」兜底：不修改密码时，写配置、代码管理、任务、升级、
// 备份等高风险操作全部被拒绝。
//
// 兼容已经被随机化的旧安装：若凭据文件仍在、且配置里用户名/口令/路径都仍是文件
// 记录的那份（说明用户从未登进过后台，否则至少改过密码或身份），则整体回退到默认
// 值并删除凭据文件；只要有一项被改过，就说明用户拿得到当前凭据，完全不动。
func ensureAndroidDefaultCredentials(c *Config) *InitialPasswordResult {
	stored := readInitialPasswordFile()
	if stored.Password == "" {
		// 全新安装（默认值本就保留）或用户已改密：保持配置原样。
		return nil
	}
	if strings.TrimSpace(c.Web.Password) != stored.Password {
		// 口令已被用户改过，凭据文件只是残留。
		ClearInitialPasswordFile()
		return nil
	}
	if stored.Username != "" && strings.TrimSpace(c.Web.Username) != stored.Username {
		// 用户名被改过：用户能登进后台，保留其配置。
		return nil
	}
	if stored.Path != "" && normalizedWebPath(c.Web.Path) != normalizedWebPath(stored.Path) {
		return nil
	}

	c.Web.Username = defaultWebUsername
	c.Web.Path = defaultWebPath
	c.Web.Password = defaultWebPassword

	res := &InitialPasswordResult{
		Username:       strings.TrimSpace(c.Web.Username),
		Password:       strings.TrimSpace(c.Web.Password),
		Path:           normalizedWebPath(c.Web.Path),
		File:           InitialPasswordFile(),
		Source:         "Android：保持公开默认凭据",
		AndroidDefault: true,
	}
	// 只有写回成功才删除凭据文件，否则下次启动仍能据此再次回退。
	if persistWebBootstrap(res.Username, res.Password, res.Path) {
		ClearInitialPasswordFile()
		res.File = ""
	}
	return res
}

// normalizedWebPath 与 web 模块的 getWebPath 保持一致：前后都带 /。
func normalizedWebPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return defaultWebPath
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if !strings.HasSuffix(p, "/") {
		p = p + "/"
	}
	return p
}

// Banner 返回给日志/控制台展示的提示文本。
func (r *InitialPasswordResult) Banner() string {
	if r == nil {
		return ""
	}
	if r.AndroidDefault {
		return fmt.Sprintf(`\n========================================================
🔐 后台凭据（Android）：%s
   访问路径: %s
   用户名:   %s
   口令:     %s
   默认口令下写配置/代码/任务等高风险操作会被锁定
   首次登录后请立即在「服务 → Web 设置」修改密码
========================================================`,
			r.Source, r.Path, r.Username, r.Password)
	}
	var fileLine string
	if r.File != "" {
		fileLine = fmt.Sprintf("   凭据文件: %s（权限 0600，含以上三项）\n", r.File)
	}
	return fmt.Sprintf(`\n========================================================
🔐 后台安全初始化：%s
   访问路径: %s   （已随机化，旧地址将失效）
   用户名:   %s
   口令:     %s
%s   登录后请立即在「服务 → Web 设置」修改为自己的密码
   （修改成功后该文件会自动删除；也可用环境变量 %s 指定口令）
========================================================`,
		r.Source, r.Path, r.Username, r.Password, fileLine, EnvWebAdminPassword)
}

// writeInitialPasswordFile 写初始凭据文件（0600），内容便于人工查看与机器复用。
func writeInitialPasswordFile(username, password, path string) {
	p := InitialPasswordFile()
	if p == "" {
		return
	}
	content := fmt.Sprintf(`# TVGate 后台初始凭据（首次启动自动生成，请勿长期依赖）
# 登录后请立即在「服务 → Web 设置」修改为自己的密码，修改成功后本文件会自动删除
# 访问地址：http://<IP>:<端口>%s
username: %s
password: %s
path: %s
生成时间: %s
`, path, username, password, path, time.Now().Format("2006-01-02 15:04:05"))
	if err := os.WriteFile(p, []byte(content), 0o600); err == nil {
		_ = os.Chmod(p, 0o600) // 覆盖已有文件时确保权限仍是 0600
	}
}

// persistWebBootstrap 把随机化的用户名/口令/路径写回配置文件
// （只替换 web 段里的对应节点，保留其余内容与注释）。
// 返回是否成功写回：Android 回退默认值后要据此决定能否删除凭据文件。
func persistWebBootstrap(username, password, path string) bool {
	if ConfigFilePath == nil || *ConfigFilePath == "" {
		return false
	}
	file := *ConfigFilePath
	data, err := os.ReadFile(file)
	if err != nil {
		return false
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return false
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return false
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return false
	}

	setScalar := func(webNode *yaml.Node, key, val string) {
		for i := 0; i+1 < len(webNode.Content); i += 2 {
			if webNode.Content[i].Value == key {
				webNode.Content[i+1] = &yaml.Node{
					Kind: yaml.ScalarNode, Tag: "!!str", Style: yaml.DoubleQuotedStyle, Value: val,
				}
				return
			}
		}
		webNode.Content = append(webNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: key},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Style: yaml.DoubleQuotedStyle, Value: val})
	}

	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "web" {
			continue
		}
		webNode := root.Content[i+1]
		if webNode.Kind != yaml.MappingNode {
			return false
		}
		setScalar(webNode, "username", username)
		setScalar(webNode, "password", password)
		setScalar(webNode, "path", path)
		out, err := yaml.Marshal(&doc)
		if err != nil {
			return false
		}
		if err := os.WriteFile(file, out, 0o600); err != nil {
			return false
		}
		return true
	}
	return false
}
