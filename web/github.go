package web

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/updater"
)

// 注册 GitHub 升级接口
func RegisterGithubRoutes(mux *http.ServeMux, webPath string, cookieAuth func(http.HandlerFunc) http.HandlerFunc) {
	if webPath == "" {
		webPath = "/web/"
	}
	if webPath[len(webPath)-1] != '/' {
		webPath += "/"
	}
	githubPath := webPath + "github/"

	mux.HandleFunc(githubPath+"releases", cookieAuth(handleGithubReleases))
	mux.HandleFunc(githubPath+"update", cookieAuth(handleGithubUpdate))
	mux.HandleFunc(githubPath+"status", cookieAuth(handleGithubStatus))
}

// 获取 GitHub Releases 列表
func handleGithubReleases(w http.ResponseWriter, r *http.Request) {
	cfg := config.Cfg.Github
	releases, err := updater.FetchGithubReleases(cfg)
	if err != nil {
		http.Error(w, fmt.Sprintf("获取版本列表失败: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(releases)
}

// 异步升级指定版本
func handleGithubUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Version == "" {
		http.Error(w, "请求参数错误", http.StatusBadRequest)
		return
	}

	go func() {
		err := updater.UpdateFromGithub(config.Cfg.Github, req.Version)
		if err != nil {
			updater.SetStatus("error", err.Error())
		} else {
			updater.SetStatus("success", fmt.Sprintf("已升级到版本 %s", req.Version))
		}
	}()

	updater.SetStatus("running", fmt.Sprintf("正在升级到版本 %s", req.Version))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "message": "开始升级"})
}

// 获取升级状态
func handleGithubStatus(w http.ResponseWriter, r *http.Request) {
	status := updater.GetStatus()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}
