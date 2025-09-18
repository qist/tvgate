package updater

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

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
type Release struct {
	TagName string `json:"tag_name"`
}

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

func FetchGithubReleases(cfg config.GithubConfig) ([]Release, error) {
	apiURL := "https://api.github.com/repos/qist/tvgate/releases"
	if cfg.Enabled && cfg.URL != "" {
		apiURL = buildURL(cfg.URL, apiURL)
	}

	client := &http.Client{Timeout: cfg.Timeout}
	if cfg.Timeout == 0 {
		client.Timeout = http.DefaultClient.Timeout
	}

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "TVGate-Updater")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("请求返回错误状态码 %d", resp.StatusCode)
	}

	var releases []Release
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}

	// 简化只返回 TagName
	simplified := make([]Release, len(releases))
	for i, r := range releases {
		simplified[i] = Release{TagName: r.TagName}
	}
	return simplified, nil
}

// --- 系统架构 ---
type ArchInfo struct {
	GOOS        string
	GOARCH      string
	GOARM       string
	PackageArch string
}

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

// --------------------
// 下载 + 解压 + 平滑升级
// --------------------
func UpdateFromGithub(cfg config.GithubConfig, version string) error {
	SetStatus("starting", "开始升级流程")
	fmt.Println("开始升级到版本:", version)

	arch, err := GetArchInfo()
	if err != nil {
		SetStatus("error", fmt.Sprintf("获取系统架构信息失败: %v", err))
		return err
	}

	zipFileName := fmt.Sprintf("TVGate-%s-%s.zip", arch.GOOS, arch.PackageArch)
	urls := getDownloadURLs(cfg, version, zipFileName)
	tmpFile := filepath.Join(os.TempDir(), zipFileName)

	SetStatus("downloading", "开始下载")
	success := false
	var lastErr error
	for i, u := range urls {
		fmt.Printf("下载源 #%d: %s\n", i+1, u)
		if err := downloadFile(u, tmpFile); err != nil {
			fmt.Printf("下载失败: %v\n", err)
			lastErr = err
			continue
		}
		success = true
		break
	}
	if !success {
		SetStatus("error", fmt.Sprintf("所有下载源失败: %v", lastErr))
		return lastErr
	}

	execPath, _ := os.Executable()
	backupPath := execPath + ".bak"
	_ = copyFile(execPath, backupPath)
	fmt.Println("备份完成:", backupPath)

	tmpDestDir := filepath.Join(filepath.Dir(execPath), ".tmp_upgrade")
	_ = os.RemoveAll(tmpDestDir)
	_ = os.MkdirAll(tmpDestDir, 0755)

	if err := unzip(tmpFile, tmpDestDir); err != nil {
		return err
	}
	newExecPath := filepath.Join(tmpDestDir, filepath.Base(execPath))
	if runtime.GOOS != "windows" {
		_ = os.Chmod(newExecPath, 0755)
	}

	// 调用升级子进程处理替换和启动
	// 注意：传给RunUpgrader的newExecPath应该是临时目录中的新可执行文件路径
	 upgrade.RunUpgrader(execPath, newExecPath, *config.ConfigFilePath, tmpDestDir)

	// 通知旧程序退出
	_ = upgrade.NotifyUpgradeReady()

	return nil
}

// --------------------
// 工具函数
// --------------------
func downloadFile(url, dst string) error {
	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Get(url)
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

func copyFile(src, dst string) error {
	in, _ := os.Open(src)
	defer in.Close()
	out, _ := os.Create(dst)
	defer out.Close()
	_, err := io.Copy(out, in)
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
			_ = os.MkdirAll(path, f.Mode())
			continue
		}
		_ = os.MkdirAll(filepath.Dir(path), 0755)
		rc, _ := f.Open()
		out, _ := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
		_, _ = io.Copy(out, rc)
		rc.Close()
		out.Close()
	}
	return nil
}
