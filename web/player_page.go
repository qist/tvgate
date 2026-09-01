package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed player
var playerPageFS embed.FS

// PlayerPageHandler 提供 H5 播放器页面与静态库（hls.js / mpegts.js / artplayer.js），同源随二进制发布。
// 页面随二进制内嵌更新，必须禁缓存，避免浏览器用旧版 index.html 修复看不到。
func PlayerPageHandler() http.Handler {
	sub, err := fs.Sub(playerPageFS, "player")
	if err != nil {
		return http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store, must-revalidate")
		http.FileServer(http.FS(sub)).ServeHTTP(w, r)
	})
}