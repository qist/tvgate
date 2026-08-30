package sync

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/updater"
)

// GitHubClient 通过 GitHub REST API（git/trees + git/blobs）访问仓库。
// 认证：Authorization: Bearer {PAT}；fine-grained token 需 Contents: Read。
type GitHubClient struct {
	cfg           config.SyncConfig
	github        config.GithubConfig
	client        *http.Client
	archiveClient *http.Client // 归档等大文件下载用宽松超时（sync.timeout 仅适合普通 API 请求）
	baseURL       string       // 默认 https://api.github.com（测试可注入）
}

// archiveDownloadTimeout 整仓归档下载超时（大仓库可达数十 MB，远超 sync.timeout）
const archiveDownloadTimeout = 10 * time.Minute

// NewGitHubClient 创建 GitHub 客户端，复用 github 加速配置。
func NewGitHubClient(syncCfg config.SyncConfig, githubCfg config.GithubConfig) *GitHubClient {
	timeout := syncCfg.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &GitHubClient{
		cfg:           syncCfg,
		github:        githubCfg,
		client:        &http.Client{Timeout: timeout},
		archiveClient: &http.Client{Timeout: archiveDownloadTimeout},
		baseURL:       "https://api.github.com",
	}
}

func (g *GitHubClient) RepoID() string { return g.cfg.Repo }

// apiURLs 组装请求地址列表：github.enabled 时优先主/备用加速地址，最后兜底官方地址。
func (g *GitHubClient) apiURLs(apiPath string) []string {
	var urls []string
	if g.github.Enabled {
		if g.github.URL != "" {
			urls = append(urls, updater.BuildURL(g.github.URL, apiPath))
		}
		for _, b := range g.github.BackupURLs {
			if b != "" {
				urls = append(urls, updater.BuildURL(b, apiPath))
			}
		}
	}
	urls = append(urls, apiPath)
	return urls
}

func (g *GitHubClient) do(rawURL string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "TVGate-Sync")
	if g.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.cfg.Token)
	}
	return g.client.Do(req)
}

// Tree 拉取仓库递归目录树，按 prefix（repo_path）过滤出目标目录并去掉前缀。
func (g *GitHubClient) Tree(branch, prefix string) ([]FileNode, error) {
	apiPath := fmt.Sprintf("%s/repos/%s/git/trees/%s?recursive=1",
		g.baseURL, g.cfg.Repo, url.PathEscape(branch))
	var lastErr error
	for _, u := range g.apiURLs(apiPath) {
		resp, err := g.do(u)
		if err != nil {
			lastErr = err
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("GitHub 树请求失败 %s: %d %s", u, resp.StatusCode, truncate(string(body), 300))
			continue
		}
		var treeResp struct {
			Tree []struct {
				Path string `json:"path"`
				Type string `json:"type"`
				SHA  string `json:"sha"`
			} `json:"tree"`
		}
		if err := json.Unmarshal(body, &treeResp); err != nil {
			lastErr = err
			continue
		}
		var nodes []FileNode
		for _, t := range treeResp.Tree {
			if t.Type != "blob" {
				continue
			}
			rel, ok := stripPrefix(t.Path, prefix)
			if !ok {
				continue
			}
			nodes = append(nodes, FileNode{Path: rel, SHA: t.SHA, Mode: t.Type})
		}
		return nodes, nil
	}
	return nil, lastErr
}

// Fetch 按 blob sha 取文件内容（GitHub 返回 base64，自动解码）。
// ref 参数为 Tree 返回的 blob sha。
func (g *GitHubClient) Fetch(path, ref string) ([]byte, error) {
	apiPath := fmt.Sprintf("%s/repos/%s/git/blobs/%s",
		g.baseURL, g.cfg.Repo, url.PathEscape(ref))
	var lastErr error
	for _, u := range g.apiURLs(apiPath) {
		resp, err := g.do(u)
		if err != nil {
			lastErr = err
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("GitHub blob 请求失败 %s: %d %s", u, resp.StatusCode, truncate(string(body), 300))
			continue
		}
		var blobResp struct {
			Content  string `json:"content"`
			Encoding string `json:"encoding"`
		}
		if err := json.Unmarshal(body, &blobResp); err != nil {
			lastErr = err
			continue
		}
		if blobResp.Encoding == "base64" {
			data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(blobResp.Content, "\n", ""))
			if err != nil {
				lastErr = err
				continue
			}
			return data, nil
		}
		return []byte(blobResp.Content), nil
	}
	return nil, lastErr
}

// Archive 下载整个仓库的 tar.gz 归档（使用宽松超时的 archiveClient，大仓库下载可能耗时较长）：
//   - 公开仓库（无 token）：直接走 codeload.github.com，不占用 api.github.com 未认证 60 次/小时的限额；
//   - 私有仓库（有 token）：走 API tarball 端点（返回 302 自动跟随到 codeload 签名地址），1 次请求/轮询。
func (g *GitHubClient) Archive(branch string) ([]byte, error) {
	if g.cfg.Token == "" {
		u := fmt.Sprintf("https://codeload.github.com/%s/tar.gz/refs/heads/%s",
			g.cfg.Repo, url.PathEscape(branch))
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "TVGate-Sync")
		resp, err := g.archiveClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("GitHub codeload 归档请求失败 %s: %d", u, resp.StatusCode)
		}
		return body, nil
	}

	apiPath := fmt.Sprintf("%s/repos/%s/tarball/%s",
		g.baseURL, g.cfg.Repo, url.PathEscape(branch))
	var lastErr error
	for _, u := range g.apiURLs(apiPath) {
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("User-Agent", "TVGate-Sync")
		if g.cfg.Token != "" {
			req.Header.Set("Authorization", "Bearer "+g.cfg.Token)
		}
		resp, err := g.archiveClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("GitHub archive 请求失败 %s: %d %s", u, resp.StatusCode, truncate(string(body), 300))
			continue
		}
		return body, nil
	}
	return nil, lastErr
}

// stripPrefix 去掉 repo_path 前缀，返回相对路径。prefix 为空或 "." 表示仓库根。
func stripPrefix(path, prefix string) (string, bool) {
	if prefix == "" || prefix == "." {
		return path, true
	}
	prefix = strings.TrimSuffix(prefix, "/")
	if path == prefix {
		return "", false // prefix 本身是目录节点，忽略
	}
	if strings.HasPrefix(path, prefix+"/") {
		return strings.TrimPrefix(path, prefix+"/"), true
	}
	return "", false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
