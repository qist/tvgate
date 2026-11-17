package web

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
)

// 弱口令保护：后台仍使用默认口令（内嵌配置 admin/admin）或明显弱口令时，
// 锁定所有高危/敏感操作（改 PHP 目录、定时任务、整份 YAML、代码管理、
// 仓库同步、升级、备份、日志等），必须先到「Web 设置」改密码才能解锁。
// 目的：默认口令是公开信息，未改密码的后台等同于对全网开放 RCE/文件管理。

const (
	// defaultWebPassword 首次启动内嵌配置 web/config.yaml 的默认口令
	defaultWebPassword = "admin"
	// minWebPasswordLen 新口令最小长度
	minWebPasswordLen = 8
)

// commonWeakPasswords 常见弱口令（小写比较，整串匹配）。
var commonWeakPasswords = map[string]struct{}{
	"admin": {}, "admin123": {}, "admin888": {}, "administrator": {},
	"123456": {}, "1234567": {}, "12345678": {}, "123456789": {}, "1234567890": {},
	"111111": {}, "000000": {}, "666666": {}, "888888": {}, "123123": {},
	"password": {}, "passwd": {}, "qwerty": {}, "qwerty123": {},
	"abc123": {}, "letmein": {}, "welcome": {}, "iloveyou": {},
	"root": {}, "toor": {}, "tvgate": {}, "tvbox": {},
}

// isWeakWebPassword 判断 Web 后台口令是否为默认/弱口令。
// 空口令返回 false：空口令时登录会被拒绝（后台不可用），不属于可被默认口令利用的场景。
func isWeakWebPassword(username, password string) (bool, string) {
	pw := strings.TrimSpace(password)
	if pw == "" {
		return false, ""
	}
	lower := strings.ToLower(pw)
	switch {
	case lower == defaultWebPassword:
		return true, "当前仍是默认密码 admin"
	case username != "" && strings.EqualFold(pw, strings.TrimSpace(username)):
		return true, "当前密码与用户名相同"
	case len([]rune(pw)) < minWebPasswordLen:
		return true, fmt.Sprintf("当前密码长度不足 %d 位", minWebPasswordLen)
	}
	if _, ok := commonWeakPasswords[lower]; ok {
		return true, "当前密码属于常见弱口令"
	}
	return false, ""
}

// weakPasswordState 返回当前后台口令是否弱口令及原因（读取实时配置，改密后立即生效）。
func weakPasswordState() (bool, string) {
	config.CfgMu.RLock()
	enabled := config.Cfg.Web.Enabled
	username := config.Cfg.Web.Username
	password := config.Cfg.Web.Password
	config.CfgMu.RUnlock()

	if !enabled {
		return false, ""
	}
	return isWeakWebPassword(username, password)
}

// lockedUnderWeakPassword 判断相对 webPath 的请求路径是否属于弱口令下必须锁定的操作。
// 规则：允许改密码本身与只读的模块配置，锁定全部写配置、代码、任务、同步、升级、备份、日志、DNS。
func lockedUnderWeakPassword(rel string) bool {
	rel = strings.Trim(strings.TrimSpace(rel), "/")
	if rel == "" {
		return false
	}
	// 改密码是唯一出路：读取/保存 Web 设置始终放行
	if rel == "config/web" || rel == "config/save-web" {
		return false
	}
	switch {
	case rel == "config", // 整份 YAML（含全部密钥）
		rel == "config/save",                   // 整份 YAML 保存
		strings.HasPrefix(rel, "config/save-"), // 各模块写配置
		strings.HasPrefix(rel, "config/backup/"),
		strings.HasPrefix(rel, "api/code/"),
		strings.HasPrefix(rel, "api/tasks/"),
		strings.HasPrefix(rel, "api/sync/"),
		strings.HasPrefix(rel, "api/github/"),
		strings.HasPrefix(rel, "api/backup/"),
		strings.HasPrefix(rel, "api/logs/"),
		strings.HasPrefix(rel, "github/"),
		strings.HasPrefix(rel, "api/dns/"),
		rel == "config/test-endpoint":
		return true
	}
	return false
}

// checkWeakPasswordGuard 在 cookieAuth 中调用：弱口令 + 命中高危路径时写 403 并返回 false。
func (h *ConfigHandler) checkWeakPasswordGuard(w http.ResponseWriter, r *http.Request) bool {
	weak, hint := weakPasswordState()
	if !weak {
		return true
	}
	rel := strings.TrimPrefix(r.URL.Path, h.getWebPath())
	if !lockedUnderWeakPassword(rel) {
		return true
	}

	ip := r.RemoteAddr
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}
	logger.LogPrintf("🔒 [web] 弱口令拦截高危请求（%s）: %s %s from %s", hint, r.Method, r.URL.Path, ip)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"code":          403,
		"weak_password": true,
		"msg": "安全限制：" + hint +
			"，修改 PHP 目录、定时任务、配置文件、代码文件、仓库同步、升级与备份等高危操作已锁定，请先在「Web 设置」修改密码",
	})
	return false
}
