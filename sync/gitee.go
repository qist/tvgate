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
)

// GiteeClient 通过 Gitee API v5 访问仓库（分支/树/原始内容/归档）。
// 认证：Authorization: token {PAT}（Gitee 私人令牌），归档下载地址额外带 access_token。
type GiteeClient struct {
	cfg           config.SyncConfig
	client        *http.Client
	archiveClient *http.Client // 归档等大文件下载用宽松超时
	host          string       // 默认 https://gitee.com
}

// NewGiteeClient 创建 Gitee 客户端；host 为空默认 gitee.com。
func NewGiteeClient(syncCfg config.SyncConfig) *GiteeClient {
	timeout := syncCfg.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	host := syncCfg.Host
	if host == "" {
		host = "https://gitee.com"
	}
	return &GiteeClient{
		cfg:           syncCfg,
		client:        &http.Client{Timeout: timeout},
		archiveClient: &http.Client{Timeout: archiveDownloadTimeout},
		host:          strings.TrimSuffix(host, "/"),
	}
}

func (g *GiteeClient) RepoID() string { return g.cfg.Repo }

// do 发起请求：追加 access_token 查询参数（Gitee 官方鉴权方式，兼容公开/私有仓库）
func (g *GiteeClient) do(rawURL string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, g.authed(rawURL), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "TVGate-Sync")
	return g.client.Do(req)
}

// authed 追加 access_token 查询参数（Gitee 使用 access_token 而非 Bearer 头）
func (g *GiteeClient) authed(rawURL string) string {
	if g.cfg.Token == "" {
		return rawURL
	}
	sep := "?"
	if strings.Contains(rawURL, "?") {
		sep = "&"
	}
	return rawURL + sep + "access_token=" + url.QueryEscape(g.cfg.Token)
}

// branchSHA 获取分支头 commit sha（Gitee 树接口按 sha 拉取）
func (g *GiteeClient) branchSHA(branch string) (string, error) {
	u := g.host + "/api/v5/repos/" + g.cfg.Repo + "/branches/" + url.PathEscape(branch)
	resp, err := g.do(u)
	if err != nil {
		return "", err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Gitee 分支请求失败 %s: %d %s", u, resp.StatusCode, truncate(string(body), 300))
	}
	var br struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(body, &br); err != nil {
		return "", err
	}
	if br.Commit.SHA == "" {
		return "", fmt.Errorf("Gitee 分支 %s 无 commit sha", branch)
	}
	return br.Commit.SHA, nil
}

// Tree 拉取递归目录树（先取分支头 sha，再取树），按 prefix 过滤并去掉前缀。
func (g *GiteeClient) Tree(branch, prefix string) ([]FileNode, error) {
	sha, err := g.branchSHA(branch)
	if err != nil {
		return nil, err
	}
	u := g.host + "/api/v5/repos/" + g.cfg.Repo + "/git/trees/" + url.PathEscape(sha) + "?recursive=1"
	resp, err := g.do(u)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Gitee 树请求失败 %s: %d %s", u, resp.StatusCode, truncate(string(body), 300))
	}
	var treeResp struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"tree"`
	}
	if err := json.Unmarshal(body, &treeResp); err != nil {
		return nil, err
	}
	var nodes []FileNode
	for _, t := range treeResp.Tree {
		if t.Type != "blob" {
			continue
		}
		if rel, ok := stripPrefix(t.Path, prefix); ok {
			nodes = append(nodes, FileNode{Path: rel, SHA: t.SHA, Mode: t.Type})
		}
	}
	return nodes, nil
}

// Fetch 按 blob sha 取文件内容（git/blobs 公开仓库免 token，返回 base64）。
// ref 参数为 Tree 返回的 blob sha；私有仓库自动带 access_token。
func (g *GiteeClient) Fetch(path, ref string) ([]byte, error) {
	// Gitee 的 raw 接口即使公开仓库也要求登录，改用 git/blobs/{sha} 即可公开读取
	u := g.host + "/api/v5/repos/" + g.cfg.Repo + "/git/blobs/" + url.PathEscape(ref)
	resp, err := g.do(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Gitee blob 请求失败 %s: %d %s", path, resp.StatusCode, truncate(string(body), 300))
	}
	var blob struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := json.Unmarshal(body, &blob); err != nil {
		return nil, err
	}
	if blob.Encoding == "base64" {
		data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(blob.Content, "\n", ""))
		if err != nil {
			return nil, err
		}
		return data, nil
	}
	return []byte(blob.Content), nil
}

// Archive 下载整仓归档（Gitee web 下载地址，返回 zip；私有仓库带 access_token）。
func (g *GiteeClient) Archive(branch string) ([]byte, error) {
	u := g.authed(g.host + "/" + g.cfg.Repo + "/repository/archive/" + url.PathEscape(branch) + ".zip")
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
		return nil, fmt.Errorf("Gitee 归档请求失败 %s: %d", u, resp.StatusCode)
	}
	return body, nil
}
