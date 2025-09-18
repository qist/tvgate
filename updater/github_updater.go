package updater

import (
	"archive/zip"
	"encoding/json"
	// "errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	// "os/exec"
	"path/filepath"
	"runtime"
	// "syscall"
	// "strings"
	"sync"
	// "time"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/utils/upgrade"
)

var (
	statusMutex sync.RWMutex
	statusMap   = map[string]string{"state": "idle", "message": ""}
)

// --- 升级状态管理 ---
func SetStatus(state, message string) {
	statusMutex.Lock()
	defer statusMutex.Unlock()
	statusMap["state"] = state
	statusMap["message"] = message
}

func GetStatus() map[string]string {
	statusMutex.RLock()
	defer statusMutex.RUnlock()
	cpy := make(map[string]string)
	for k, v := range statusMap {
		cpy[k] = v
	}
	return cpy
}

// --- Release 信息 ---
// Release GitHub Release 信息（简化版，只返回版本号）
type Release struct {
	TagName string `json:"tag_name"`
}

// buildURL 安全拼接 base 与 target
func buildURL(base, target string) string {
	u, err := url.Parse(base)
	if err != nil {
		return target
	}
	t, err := url.Parse(target)
	if err != nil {
		return target
	}
	return u.ResolveReference(t).String()
}

// FetchGithubReleases 获取版本列表
func FetchGithubReleases(cfg config.GithubConfig) ([]Release, error) {
	origURL := "https://api.github.com/repos/qist/tvgate/releases"
	apiURL := origURL

	// 创建带超时的HTTP客户端
	client := &http.Client{Timeout: cfg.Timeout}
	if cfg.Timeout == 0 {
		client.Timeout = http.DefaultClient.Timeout
	}

	if cfg.Enabled && cfg.URL != "" {
		apiURL = buildURL(cfg.URL, origURL)
	}

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %v", err)
	}

	// 添加User-Agent头部，避免被GitHub API拒绝
	req.Header.Set("User-Agent", "TVGate-Updater")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败 (%s): %v", apiURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("请求返回错误状态码 %d (%s)", resp.StatusCode, resp.Status)
	}

	var releases []Release
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("解析响应失败: %v", err)
	}

	// 仅返回 TagName
	simplified := make([]Release, len(releases))
	for i, r := range releases {
		simplified[i] = Release{TagName: r.TagName}
	}
	return simplified, nil
}

// --- 系统架构信息 ---
type ArchInfo struct {
	GOOS        string
	GOARCH      string
	GOARM       string
	PackageArch string
}

// GetArchInfo 获取当前系统架构
func GetArchInfo() (*ArchInfo, error) {
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	var arch ArchInfo

	switch fmt.Sprintf("%s-%s", goos, goarch) {
	case "linux-amd64":
		arch = ArchInfo{"linux", "amd64", "", "amd64"}
	case "linux-arm64":
		arch = ArchInfo{"linux", "arm64", "", "arm64"}
	case "linux-arm":
		goarm := os.Getenv("GOARM")
		if goarm == "" {
			goarm = "7"
		}
		arch = ArchInfo{"linux", "arm", goarm, "armv" + goarm}
	case "linux-386":
		arch = ArchInfo{"linux", "386", "", "386"}
	case "windows-amd64":
		arch = ArchInfo{"windows", "amd64", "", "amd64"}
	case "windows-386":
		arch = ArchInfo{"windows", "386", "", "386"}
	case "darwin-amd64":
		arch = ArchInfo{"darwin", "amd64", "", "amd64"}
	case "darwin-arm64":
		arch = ArchInfo{"darwin", "arm64", "", "arm64"}
	default:
		return nil, fmt.Errorf("unsupported OS/ARCH: %s-%s", goos, goarch)
	}
	return &arch, nil
}

// getDownloadURLs 构建下载 URL 列表（加速 + 备用 + 原始）
func getDownloadURLs(cfg config.GithubConfig, version, zipFileName string) []string {
	urls := []string{}
	origURL := fmt.Sprintf("https://github.com/qist/tvgate/releases/download/%s/%s", version, zipFileName)

	if cfg.Enabled {
		if cfg.URL != "" {
			urls = append(urls, buildURL(cfg.URL, origURL))
		}
		for _, b := range cfg.BackupURLs {
			if b != "" {
				urls = append(urls, buildURL(b, origURL))
			}
		}
	}
	urls = append(urls, origURL)
	return urls
}

// UpdateFromGithub 下载指定版本并替换当前程序
// --- UpdateFromGithub ---
func UpdateFromGithub(cfg config.GithubConfig, version string) error {
	SetStatus("downloading", "开始下载")

	arch, err := GetArchInfo()
	if err != nil {
		SetStatus("error", err.Error())
		return err
	}

	zipFileName := fmt.Sprintf("TVGate-%s-%s.zip", arch.GOOS, arch.PackageArch)
	urls := getDownloadURLs(cfg, version, zipFileName)
	tmpFile := filepath.Join(os.TempDir(), zipFileName)

	var lastErr error
	for _, url := range urls {
		fmt.Println("Downloading:", url)
		if err := downloadFile(url, tmpFile); err != nil {
			fmt.Println("Failed:", err)
			lastErr = err
			continue
		}
		lastErr = nil
		break
	}
	if lastErr != nil {
		SetStatus("error", "下载失败")
		return fmt.Errorf("all download attempts failed: %v", lastErr)
	}

	SetStatus("backing_up", "备份当前程序")
	execPath, err := os.Executable()
	if err != nil {
		SetStatus("error", err.Error())
		return err
	}
	backupPath := execPath + ".bak"
	if err := copyFile(execPath, backupPath); err != nil {
		SetStatus("error", err.Error())
		return err
	}

	SetStatus("unzipping", "解压新版本")
	destDir := filepath.Dir(execPath)
	if err := unzip(tmpFile, destDir); err != nil {
		SetStatus("error", err.Error())
		return err
	}

	// 确保可执行权限（Linux/macOS）
	if runtime.GOOS != "windows" {
		if err := os.Chmod(execPath, 0755); err != nil {
			SetStatus("error", err.Error())
			return err
		}
	}
	// 通知旧程序退出
	upgrade.NotifyUpgradeReady()
	SetStatus("restarting", "启动新程序")
	return upgrade.RestartProcess(execPath)
}

// --- 下载文件 ---
func downloadFile(url, dst string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	return err
}

func unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		path := filepath.Join(dest, f.Name)
		if f.FileInfo().IsDir() {
			os.MkdirAll(path, f.Mode())
			continue
		}
		os.MkdirAll(filepath.Dir(path), 0755)
		rc, _ := f.Open()
		out, _ := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
		_, err := io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return err
		}
	}
	return nil
}


// --- 复制文件 ---
func copyFile(src, dst string) error {
	in, _ := os.Open(src)
	defer in.Close()
	out, _ := os.Create(dst)
	defer out.Close()
	_, err := io.Copy(out, in)
	return err
}