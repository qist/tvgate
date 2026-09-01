package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed player
var playerPageFS embed.FS

// PlayerPageHandler 提供 H5 播放器页面与静态库（hls.js / mpegts.js），同源随二进制发布。
func PlayerPageHandler() http.Handler {
	sub, err := fs.Sub(playerPageFS, "player")
	if err != nil {
		return http.NotFoundHandler()
	}
	return http.FileServer(http.FS(sub))
}