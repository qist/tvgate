package updater

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"sync"

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
func UpdateFromGithub(cfg config.GithubConfig, version string) error {
	SetStatus("starting", "开始升级流程")
	fmt.Printf("开始升级到版本: %s\n", version)

	arch, err := GetArchInfo()
	if err != nil {
		SetStatus("error", fmt.Sprintf("获取系统架构信息失败: %v", err))
		fmt.Printf("获取系统架构信息失败: %v\n", err)
		return err
	}
	
	fmt.Printf("系统架构: GOOS=%s, GOARCH=%s, PackageArch=%s\n", arch.GOOS, arch.GOARCH, arch.PackageArch)

	zipFileName := fmt.Sprintf("TVGate-%s-%s.zip", arch.GOOS, arch.PackageArch)
	urls := getDownloadURLs(cfg, version, zipFileName)
	tmpFile := filepath.Join(os.TempDir(), zipFileName)
	
	fmt.Printf("准备下载文件: %s\n", zipFileName)
	fmt.Printf("下载URL列表: %v\n", urls)

	SetStatus("downloading", "开始下载")
	var lastErr error
	success := false
	for i, url := range urls {
		fmt.Printf("尝试下载源 #%d: %s\n", i+1, url)
		SetStatus("downloading", fmt.Sprintf("正在从源 #%d 下载...", i+1))
		if err := downloadFile(url, tmpFile); err != nil {
			fmt.Printf("从源 #%d 下载失败: %v\n", i+1, err)
			lastErr = err
			SetStatus("downloading", fmt.Sprintf("从源 #%d 下载失败: %v", i+1, err))
			continue
		}
		success = true
		fmt.Printf("从源 #%d 下载完成\n", i+1)
		SetStatus("downloading", fmt.Sprintf("从源 #%d 下载完成", i+1))
		break
	}
	if !success {
		SetStatus("error", fmt.Sprintf("所有下载源都失败: %v", lastErr))
		fmt.Printf("所有下载源都失败: %v\n", lastErr)
		// 清理临时文件
		_ = os.Remove(tmpFile)
		return fmt.Errorf("all download attempts failed: %v", lastErr)
	}

	SetStatus("backing_up", "备份当前程序")
	fmt.Println("开始备份当前程序")
	execPath, err := os.Executable()
	if err != nil {
		SetStatus("error", fmt.Sprintf("获取当前程序路径失败: %v", err))
		fmt.Printf("获取当前程序路径失败: %v\n", err)
		// 即使无法获取当前程序路径，也尝试清理临时文件
		_ = os.Remove(tmpFile)
		return err
	}
	fmt.Printf("当前程序路径: %s\n", execPath)
	
	backupPath := execPath + ".bak"
	fmt.Printf("备份路径: %s\n", backupPath)
	if err := copyFile(execPath, backupPath); err != nil {
		SetStatus("error", fmt.Sprintf("备份当前程序失败: %v", err))
		fmt.Printf("备份当前程序失败: %v\n", err)
		// 即使备份失败，也继续升级过程，但要清理临时文件
		_ = os.Remove(tmpFile)
		// 这里我们返回错误，因为备份失败可能意味着无法回滚
		return err
	}
	fmt.Println("程序备份完成")

	SetStatus("unzipping", "解压新版本")
	fmt.Println("开始解压新版本")
	destDir := filepath.Dir(execPath)
	fmt.Printf("解压目标目录: %s\n", destDir)
	
	// 创建临时目录用于解压
	tmpDestDir := filepath.Join(destDir, ".tmp_upgrade")
	fmt.Printf("临时解压目录: %s\n", tmpDestDir)
	
	// 确保临时目录存在并为空
	if err := os.RemoveAll(tmpDestDir); err != nil {
		fmt.Printf("清理临时目录失败: %v\n", err)
	}
	if err := os.MkdirAll(tmpDestDir, 0755); err != nil {
		SetStatus("error", fmt.Sprintf("创建临时目录失败: %v", err))
		fmt.Printf("创建临时目录失败: %v\n", err)
		_ = os.Remove(tmpFile)
		return err
	}
	
	// 解压到临时目录
	if err := unzip(tmpFile, tmpDestDir); err != nil {
		SetStatus("error", fmt.Sprintf("解压新版本失败: %v", err))
		fmt.Printf("解压新版本失败: %v\n", err)
		// 解压失败，清理临时文件和目录
		_ = os.Remove(tmpFile)
		_ = os.RemoveAll(tmpDestDir)
		return err
	}
	fmt.Println("解压完成")
	
	// 获取新程序文件路径
	newExecPath := filepath.Join(tmpDestDir, filepath.Base(execPath))
	fmt.Printf("新程序路径: %s\n", newExecPath)
	
	// 检查新程序是否存在
	if _, err := os.Stat(newExecPath); os.IsNotExist(err) {
		SetStatus("error", "新程序文件不存在")
		fmt.Printf("新程序文件不存在: %s\n", newExecPath)
		_ = os.Remove(tmpFile)
		_ = os.RemoveAll(tmpDestDir)
		return fmt.Errorf("new executable not found: %s", newExecPath)
	}
	
	// 确保新程序可执行
	if runtime.GOOS != "windows" {
		SetStatus("setting_permissions", "设置新程序执行权限")
		fmt.Println("设置新程序执行权限")
		if err := os.Chmod(newExecPath, 0755); err != nil {
			SetStatus("warning", fmt.Sprintf("设置新程序执行权限失败: %v", err))
			fmt.Printf("警告: 设置新程序执行权限失败: %v\n", err)
		} else {
			fmt.Println("新程序执行权限设置完成")
		}
	}
	
	// 清理临时文件
	defer func() {
		fmt.Printf("清理临时文件: %s\n", tmpFile)
		if err := os.Remove(tmpFile); err != nil {
			fmt.Printf("清理临时文件失败: %v\n", err)
		}
		fmt.Printf("清理临时目录: %s\n", tmpDestDir)
		if err := os.RemoveAll(tmpDestDir); err != nil {
			fmt.Printf("清理临时目录失败: %v\n", err)
		}
	}()
	
	// 等待一段时间确保文件写入完成
	fmt.Println("等待文件系统同步...")
	time.Sleep(1 * time.Second)
	
	// 通知旧程序退出
	SetStatus("notifying", "通知旧程序退出")
	fmt.Println("通知旧程序退出")
	if err := upgrade.NotifyUpgradeReady(); err != nil {
		// 如果通知失败，记录日志但继续执行重启
		fmt.Printf("通知旧程序退出失败: %v\n", err)
		SetStatus("notifying", fmt.Sprintf("通知旧程序退出失败: %v", err))
	} else {
		fmt.Println("旧程序退出通知完成")
		SetStatus("notifying", "旧程序退出通知完成")
		// 给旧程序更多时间处理退出
		time.Sleep(2 * time.Second)
	}
	
	// 将新程序移动到正确位置
	SetStatus("moving", "移动新程序到正确位置")
	fmt.Printf("将新程序从 %s 移动到 %s\n", newExecPath, execPath)
	if err := os.Rename(newExecPath, execPath); err != nil {
		SetStatus("error", fmt.Sprintf("移动新程序失败: %v", err))
		fmt.Printf("移动新程序失败: %v\n", err)
		return err
	}
	
	// 再次确保新程序可执行（Rename操作可能会影响权限）
	if runtime.GOOS != "windows" {
		if err := os.Chmod(execPath, 0755); err != nil {
			fmt.Printf("警告: 最终权限设置失败: %v\n", err)
		}
	}
	
	SetStatus("restarting", "启动新程序")
	fmt.Println("启动新程序")
	fmt.Printf("新程序路径: %s\n", execPath)
	fmt.Printf("程序参数: %v\n", os.Args)
	
	// 使用syscall.Exec实现真正的平滑升级（类似Nginx）
	// 这会用新程序替换当前进程，而不是启动一个新进程
	// 这样可以保持监听的socket文件描述符等资源
	if runtime.GOOS != "windows" {
		// Linux/macOS 使用exec系统调用
		fmt.Println("使用exec系统调用启动新程序")
		args := make([]string, len(os.Args))
		copy(args, os.Args)
		args[0] = execPath
		
		env := os.Environ()
		
		// 清理可能存在的升级socket文件
		upgradeSocketPath := "/tmp/tvgate_upgrade.sock"
		if _, err := os.Stat(upgradeSocketPath); err == nil {
			_ = os.Remove(upgradeSocketPath)
		}
		
		if err := syscall.Exec(execPath, args, env); err != nil {
			SetStatus("error", fmt.Sprintf("exec系统调用失败: %v", err))
			fmt.Printf("exec系统调用失败: %v\n", err)
			return err
		}
	} else {
		// Windows 使用命令行方式启动
		fmt.Println("使用命令行方式启动新程序（Windows）")
		cmd := exec.Command(execPath, os.Args[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		
		if err := cmd.Start(); err != nil {
			SetStatus("error", fmt.Sprintf("启动新程序失败: %v", err))
			fmt.Printf("启动新程序失败: %v\n", err)
			return err
		}
		
		fmt.Printf("新程序已启动，PID: %d\n", cmd.Process.Pid)
		SetStatus("success", fmt.Sprintf("升级成功，新程序已启动(PID: %d)", cmd.Process.Pid))
		
		// 给新程序一些启动时间
		time.Sleep(2 * time.Second)
	}
	
	// 正常情况下不会执行到这里（syscall.Exec会替换当前进程）
	fmt.Println("旧程序退出")
	os.Exit(0)
	return nil
}

// --- 下载文件 ---
func downloadFile(url, dst string) error {
	client := &http.Client{
		Timeout: 300 * time.Second, // 5分钟超时
	}
	
	fmt.Printf("开始下载: %s\n", url)
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("HTTP请求失败: %v", err)
	}
	defer resp.Body.Close()
	
	fmt.Printf("服务器响应: %s\n", resp.Status)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP错误: %s", resp.Status)
	}
	
	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %v", err)
	}
	defer out.Close()
	
	// 获取文件大小用于进度显示
	contentLength := resp.Header.Get("Content-Length")
	var total int64
	if contentLength != "" {
		total, _ = strconv.ParseInt(contentLength, 10, 64)
		fmt.Printf("文件大小: %s\n", formatBytes(total))
	} else {
		fmt.Println("无法确定文件大小")
	}
	
	// 创建一个带进度报告的Reader
	progressReader := &ProgressReader{
		reader: resp.Body,
		total:  total,
		onProgress: func(read int64) {
			if total > 0 {
				percentage := float64(read) / float64(total) * 100
				// 每10%报告一次进度，避免日志过多
				if int(percentage)%10 == 0 {
					fmt.Printf("下载进度: %.0f%% (%s/%s)\n", percentage, formatBytes(read), formatBytes(total))
				}
			} else {
				// 每MB报告一次进度
				if read%(1024*1024) == 0 {
					fmt.Printf("已下载: %s\n", formatBytes(read))
				}
			}
		},
	}
	
	written, err := io.Copy(out, progressReader)
	if err != nil {
		return fmt.Errorf("写入文件失败: %v", err)
	}
	
	fmt.Printf("下载完成，总共写入: %s\n", formatBytes(written))
	
	// 确保数据写入磁盘
	if err := out.Sync(); err != nil {
		return fmt.Errorf("同步文件到磁盘失败: %v", err)
	}
	
	return nil
}

// ProgressReader 带进度报告的Reader
type ProgressReader struct {
	reader     io.Reader
	total      int64
	read       int64
	onProgress func(read int64)
}

func (pr *ProgressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	pr.read += int64(n)
	
	// 每次读取都调用进度回调
	if pr.onProgress != nil {
		pr.onProgress(pr.read)
	}
	
	return n, err
}

func unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	
	// 先统计总文件数用于进度显示
	totalFiles := len(r.File)
	processedFiles := 0
	
	for _, f := range r.File {
		processedFiles++
		SetStatus("unzipping", fmt.Sprintf("解压中... %d/%d", processedFiles, totalFiles))
		
		// 检查文件路径是否安全，防止目录遍历攻击
		if !filepath.IsLocal(f.Name) {
			fmt.Printf("警告: 跳过不安全路径: %s\n", f.Name)
			continue
		}
		
		path := filepath.Join(dest, f.Name)
		
		// 确保目标路径在正确的目录下
		rel, err := filepath.Rel(dest, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			fmt.Printf("警告: 跳过非法路径: %s\n", f.Name)
			continue
		}
		
		if f.FileInfo().IsDir() {
			os.MkdirAll(path, f.Mode())
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		
		out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// formatBytes 格式化字节大小
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(div), "KMGTPE"[exp])
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