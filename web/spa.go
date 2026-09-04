package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

const spaIndexPath = "dist/index.html"
const playerIndexPath = "dist/player.html"

// serveSPA 返回前端 SPA 入口（hash 路由，无需服务端 history fallback）。
// 认证交由前端：未认证时 SPA 自行跳 #/login；数据接口仍受 cookieAuth 保护。
func serveSPA(w http.ResponseWriter, r *http.Request) {
	data, err := distFS.ReadFile(spaIndexPath)
	if err != nil {
		http.Error(w, "前端资源缺失，请先构建 ui/（make web-ui）", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

// servePlayerPage 返回 H5 播放器独立入口（player.html，双入口构建产物）。
// 页面随二进制内嵌更新，必须禁缓存，避免浏览器用旧版页面。
func servePlayerPage(w http.ResponseWriter, r *http.Request) {
	data, err := distFS.ReadFile(playerIndexPath)
	if err != nil {
		http.Error(w, "播放器页面缺失，请先构建 ui/（make web-ui）", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	_, _ = w.Write(data)
}

// ServeStandalonePlayer 返回独立播放入口 handler（如挂载在 /pp）：
// 直接服务 player.html，不跳转后台路径——web.path 是隐藏路径，
// 任何重定向都会经 Location 头把后台路径名暴露给访客。
// 页面里的相对资源引用 ./assets/* 重写为 <webPath>assets/* 绝对路径，
// 使页面可挂在任意公开路径下渲染（API 为根路径挂载，不受影响）。
func ServeStandalonePlayer(webPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := distFS.ReadFile(playerIndexPath)
		if err != nil {
			http.Error(w, "播放器页面缺失，请先构建 ui/（make web-ui）", http.StatusNotFound)
			return
		}
		html := strings.ReplaceAll(string(data), "./assets/", webPath+"assets/")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store, must-revalidate")
		_, _ = w.Write([]byte(html))
	}
}

// registerSPARoutes 注册 SPA 资源：/web/ 入口 + /web/assets/* 静态产物 + /web/player 播放器入口。
func registerSPARoutes(mux *http.ServeMux, webPath string) {
	// 带 hash 的静态产物（长期缓存）
	if sub, err := fs.Sub(distFS, "dist/assets"); err == nil {
		fileServer := http.FileServer(http.FS(sub))
		mux.Handle(webPath+"assets/", http.StripPrefix(webPath+"assets/", fileServer))
	}
	// SPA 入口（精确 /web/）
	mux.HandleFunc(webPath, serveSPA)
	// H5 播放器入口（无尾斜杠：index.html 里的相对资源 ./assets/* 才能解析到 webPath/assets/）
	mux.HandleFunc(webPath+"player", servePlayerPage)
	mux.HandleFunc(webPath+"player.html", servePlayerPage)
	// 无尾斜杠访问（如 /web）时重定向到 /web/，
	// 否则 index.html 里的相对资源 ./assets/* 会解析到根路径而 404，
	// 导致 SPA 无法挂载（页面空白/无法点开）。
	noSlash := strings.TrimSuffix(webPath, "/")
	if noSlash != "" && noSlash != webPath {
		mux.HandleFunc(noSlash, func(w http.ResponseWriter, r *http.Request) {
			u := r.URL
			u.Path = noSlash + "/"
			u.RawQuery = r.URL.RawQuery
			http.Redirect(w, r, u.String(), http.StatusMovedPermanently)
		})
	}
}
