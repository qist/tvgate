package sync

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/qist/tvgate/config"
)

// GitLabClient 通过 GitLab REST API（repository/tree + files/raw）访问仓库。
// 认证：PRIVATE-TOKEN: {PAT}，权限 read_repository。
type GitLabClient struct {
	cfg           config.SyncConfig
	client        *http.Client
	archiveClient *http.Client // 归档等大文件下载用宽松超时
	host          string       // 默认 https://gitlab.com
	projID        string       // URL 编码的 group/project
}

// NewGitLabClient 创建 GitLab 客户端。
func NewGitLabClient(syncCfg config.SyncConfig) *GitLabClient {
	timeout := syncCfg.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &GitLabClient{
		cfg:           syncCfg,
		client:        &http.Client{Timeout: timeout},
		archiveClient: &http.Client{Timeout: archiveDownloadTimeout},
		host:          "https://gitlab.com",
		projID:        url.PathEscape(syncCfg.Repo),
	}
}

func (g *GitLabClient) RepoID() string { return g.cfg.Repo }

func (g *GitLabClient) do(path string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, g.host+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "TVGate-Sync")
	if g.cfg.Token != "" {
		req.Header.Set("PRIVATE-TOKEN", g.cfg.Token)
	}
	return g.client.Do(req)
}

// Tree 递归拉取仓库树（分页），按 prefix（repo_path）过滤并去掉前缀。
func (g *GitLabClient) Tree(branch, prefix string) ([]FileNode, error) {
	var nodes []FileNode
	page := 1
	for {
		path := fmt.Sprintf("/api/v4/projects/%s/repository/tree?ref=%s&recursive=true&per_page=100&page=%d",
			g.projID, url.QueryEscape(branch), page)
		resp, err := g.do(path)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("GitLab 树请求失败: %d %s", resp.StatusCode, truncate(string(body), 300))
		}
		var items []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
			Type string `json:"type"`
		}
		if err := json.Unmarshal(body, &items); err != nil {
			return nil, err
		}
		if len(items) == 0 {
			break
		}
		for _, it := range items {
			if it.Type != "blob" {
				continue
			}
			if rel, ok := stripPrefix(it.Path, prefix); ok {
				nodes = append(nodes, FileNode{Path: rel, SHA: it.ID, Mode: it.Type})
			}
		}
		next := resp.Header.Get("X-Next-Page")
		n, err := strconv.Atoi(next)
		if err != nil || n <= page {
			break
		}
		page = n
	}
	return nodes, nil
}

// Fetch 按文件路径取内容（GitLab raw 接口按 path 而非 blob id），ref 为分支名。
func (g *GitLabClient) Fetch(path, ref string) ([]byte, error) {
	p := strings.TrimPrefix(path, "/")
	u := fmt.Sprintf("/api/v4/projects/%s/repository/files/%s/raw?ref=%s",
		g.projID, url.PathEscape(p), url.QueryEscape(ref))
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
		return nil, fmt.Errorf("GitLab raw 请求失败 %s: %d %s", path, resp.StatusCode, truncate(string(body), 300))
	}
	return body, nil
}

// Archive 下载整个仓库的 tar.gz 归档（使用宽松超时的 archiveClient，一次请求拿全仓）。
func (g *GitLabClient) Archive(branch string) ([]byte, error) {
	u := fmt.Sprintf("/api/v4/projects/%s/repository/archive.tar.gz?ref=%s",
		g.projID, url.QueryEscape(branch))
	req, err := http.NewRequest(http.MethodGet, g.host+u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "TVGate-Sync")
	if g.cfg.Token != "" {
		req.Header.Set("PRIVATE-TOKEN", g.cfg.Token)
	}
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
		return nil, fmt.Errorf("GitLab archive 请求失败: %d %s", resp.StatusCode, truncate(string(body), 300))
	}
	return body, nil
}
