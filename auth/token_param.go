package auth

import (
	"net/http"
	"net/url"
	"strings"
)

// DefaultTokenParamName 全局 token 的默认查询参数名。
// 配置的 token_param_name 为空时回落到它；也是 H5 播放页（ui）在拿不到服务端注入时的兜底名。
const DefaultTokenParamName = "my_token"

// EffectiveParamName 返回该管理器实际使用的 token 查询参数名（配置为空时用默认名）。
func (tm *TokenManager) EffectiveParamName() string {
	if tm != nil && tm.TokenParamName != "" {
		return tm.TokenParamName
	}
	return DefaultTokenParamName
}

// ExtractToken 从请求中取出全局 token：先按配置的参数名取，取不到再回落到默认名。
//
// 这样 H5 播放页（写 my_token 的历史链接）与配置了自定义参数名（如 ouyyt）的部署都能用，
// 且两个名字都只是「取值的入口」——值仍要过 ValidateToken 校验，不降低安全性。
func ExtractToken(r *http.Request, tm *TokenManager) string {
	if r == nil || r.URL == nil || tm == nil {
		return ""
	}
	name := tm.EffectiveParamName()
	if v := r.URL.Query().Get(name); v != "" {
		return v
	}
	if name != DefaultTokenParamName {
		return r.URL.Query().Get(DefaultTokenParamName)
	}
	return ""
}

// SignURL 给**服务端自己拼出来的站内地址**补上授权查询参数（未启用授权时原样返回）。
//
// m3u8 重写后的分片行 / EXT-X-MAP / EXT-X-KEY 的 URI、回看签发的 play 地址，
// 都是服务端生成、交给客户端直接取的：hls.js 与第三方播放器不会像 H5 页面那样
// 自动补令牌，不签就一律 403（表现为歌单能拉到、画面出不来）。
// 静态令牌直接复用配置值；只开动态令牌时按目标路径现场签发一个短效令牌。
func (tm *TokenManager) SignURL(path string) string {
	if tm == nil || !tm.Enabled || path == "" {
		return path
	}
	tm.mu.RLock()
	token := ""
	for t := range tm.StaticTokens { // StaticTokens 的键就是令牌值
		token = t
		break
	}
	dyn := tm.DynamicConfig
	tm.mu.RUnlock()
	if token == "" && dyn != nil {
		if t, err := tm.GenerateDynamicToken(path); err == nil {
			token = t
		}
	}
	if token == "" {
		return path
	}
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + tm.EffectiveParamName() + "=" + url.QueryEscape(token)
}
