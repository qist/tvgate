package web

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/updater"
	tsync "github.com/qist/tvgate/utils/sync"
)

var githubWg tsync.WaitGroup

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

	// 平台不支持在线升级（Android APK 内置 so / Windows）：直接拒绝，防止误触发
	if !updater.Updatable() {
		http.Error(w, "当前平台不支持在线升级，请使用对应的安装包更新流程", http.StatusForbidden)
		return
	}

	// 先标记状态，避免并发多次升级
	updater.SetStatus("running", fmt.Sprintf("正在升级到版本 %s", req.Version))
	updater.SetTargetVersion(req.Version)

	version := req.Version
	githubWg.Go(func() {
		// ⛑ 防止升级 panic 杀死整个进程
		defer func() {
			if rec := recover(); rec != nil {
				updater.SetStatus("panic", fmt.Sprintf("升级过程中发生 panic: %v", rec))
			}
		}()

		err := updater.UpdateFromGithub(config.Cfg.Github, version)
		if err != nil {
			updater.SetStatus("error", err.Error())
			return
		}
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"message": "开始升级",
	})
}

// 获取升级状态（附带平台可升级标记，前端据此隐藏版本升级入口）
func handleGithubStatus(w http.ResponseWriter, r *http.Request) {
	status := updater.GetStatus()
	resp := make(map[string]interface{}, len(status)+1)
	for k, v := range status {
		resp[k] = v
	}
	resp["updatable"] = updater.Updatable()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
