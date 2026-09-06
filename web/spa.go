package web

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
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

// 独立播放页（/pp）的公开资源前缀：页面资源经此路径服务，
// 与隐藏的 web.path 完全解耦——否则 HTML 里的资源 URL 会把
// 后台路径名暴露给任何打开播放页的访客。
const standaloneAssetsPrefix = "/pp/assets/"

// ServeStandalonePlayer 返回独立播放入口 handler（挂载在 /pp）：
// 直接服务 player.html，不跳转后台路径——web.path 是隐藏路径，
// 任何重定向都会经 Location 头把后台路径名暴露给访客。
// 页面里的相对资源引用 ./assets/* 重写为 /pp/assets/* 公开路径，
// 使页面不引用任何 web.path 下的资源（API 为根路径挂载，不受影响）。
func ServeStandalonePlayer() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := distFS.ReadFile(playerIndexPath)
		if err != nil {
			http.Error(w, "播放器页面缺失，请先构建 ui/（make web-ui）", http.StatusNotFound)
			return
		}
		html := strings.ReplaceAll(string(data), "./assets/", standaloneAssetsPrefix)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store, must-revalidate")
		_, _ = w.Write([]byte(html))
	}
}

// ServePublicAssets 以公开路径服务 dist/assets（供 /pp/assets/ 等独立入口使用）。
// 仅服务带内容 hash 的静态产物，不含入口 HTML，不泄露 web.path。
func ServePublicAssets() http.HandlerFunc {
	sub, err := fs.Sub(distFS, "dist/assets")
	if err != nil {
		return func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "前端资源缺失，请先构建 ui/（make web-ui）", http.StatusNotFound)
		}
	}
	return gzipAssets(sub, http.FileServer(http.FS(sub))).ServeHTTP
}

// assetGzipContentTypes 显式声明静态产物 Content-Type：不依赖运行时系统
// mime.types，保证 .wasm/.js/.css 在任意平台返回一致类型。
var assetGzipContentTypes = map[string]string{
	".js":    "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".html":  "text/html; charset=utf-8",
	".wasm":  "application/wasm",
	".json":  "application/json; charset=utf-8",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".gif":   "image/gif",
	".ico":   "image/x-icon",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".ttf":   "font/ttf",
	".otf":   "font/otf",
	".txt":   "text/plain; charset=utf-8",
}

// gzipAssets 包装静态文件服务器：客户端接受 gzip 且存在构建期预压缩的
// <name>.gz（dist/assets 由 ui/build-post.mjs 生成；wasm 520KB→218KB）时
// 直接返回压缩字节，否则回退 http.FileServer 原样服务。
func gzipAssets(fsys fs.FS, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 统一 Vary，让缓存同时保留压缩/未压缩变体
		w.Header().Add("Vary", "Accept-Encoding")
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		rel := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if rel == "" {
			next.ServeHTTP(w, r)
			return
		}
		gzName := rel + ".gz"
		info, err := fs.Stat(fsys, gzName)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		gz, err := fs.ReadFile(fsys, gzName)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		ext := strings.ToLower(filepath.Ext(rel))
		contentType := assetGzipContentTypes[ext]
		if contentType == "" {
			contentType = mime.TypeByExtension(ext)
		}
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Length", strconv.Itoa(len(gz)))
		w.Header().Set("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(gz)
		}
	})
}

// registerSPARoutes 注册 SPA 资源：/web/ 入口 + /web/assets/* 静态产物 + /web/player 播放器入口。
func registerSPARoutes(mux *http.ServeMux, webPath string) {
	// 带 hash 的静态产物（长期缓存）；优先返回预压缩 .gz
	if sub, err := fs.Sub(distFS, "dist/assets"); err == nil {
		fileServer := gzipAssets(sub, http.FileServer(http.FS(sub)))
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
