package sync

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	gosync "sync"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/php"
)

// SyncManager 仓库同步管理器（单向：仓库 → 本地）。
type SyncManager struct {
	cfg       *config.SyncConfig
	githubCfg config.GithubConfig
	client    RepoClient
	localRoot string
	cancel    context.CancelFunc
}

// archiveThreshold 变更文件数超过该值时改用整仓归档下载（1 次请求），避免逐文件拉取触发 API 限流
const archiveThreshold = 20

var (
	managerMu gosync.Mutex
	managers  []*SyncManager
)

// Start 启动（或替换全部已有实例）仓库同步。
// 每次调用都会停止旧实例并按最新配置重启，供启动与配置热加载共用。
// 支持多仓库：config.Cfg.Sync 列表中的每个 enabled 条目各自启动一个同步循环。
func Start(c *config.Config) {
	managerMu.Lock()
	defer managerMu.Unlock()

	// 停止全部旧实例
	for _, m := range managers {
		m.stop()
	}
	managers = nil

	if len(c.Sync) == 0 {
		return
	}
	for i := range c.Sync {
		entry := &c.Sync[i]
		if !entry.Enabled {
			continue
		}
		sm := newManager(entry, c.Github)
		if sm == nil {
			continue
		}
		managers = append(managers, sm)
		go sm.loop(config.ServerCtx)
		logger.LogPrintf("🚀 [sync] 已启动: %s (branch=%s) → %s，间隔 %s",
			syncLabel(entry), entry.Branch, sm.localRoot, entry.Interval)
	}
}

// syncLabel 生成仓库标识（有 name 时用 name + repo）
func syncLabel(s *config.SyncConfig) string {
	if s.Name != "" {
		return s.Name + " (" + s.Repo + ")"
	}
	return s.Repo
}

func newManager(entry *config.SyncConfig, githubCfg config.GithubConfig) *SyncManager {
	docRoot := php.ResolvedDocRoot()
	if docRoot == "" {
		logger.LogPrintf("❌ [sync] php docroot 为空，跳过同步 %s", syncLabel(entry))
		return nil
	}
	localRoot := docRoot
	if entry.LocalPath != "" && entry.LocalPath != "." {
		localRoot = filepath.Join(docRoot, entry.LocalPath)
	}
	// 防穿越：解析后必须仍以 docroot 为前缀
	if localRoot != docRoot && !strings.HasPrefix(localRoot, docRoot+string(filepath.Separator)) {
		logger.LogPrintf("❌ [sync] local_path 不在 docroot 内: %s", entry.LocalPath)
		return nil
	}
	if err := os.MkdirAll(localRoot, 0755); err != nil {
		logger.LogPrintf("❌ [sync] 创建目标目录失败: %v", err)
		return nil
	}
	sm := &SyncManager{
		cfg:       entry,
		githubCfg: githubCfg,
		localRoot: filepath.Clean(localRoot),
	}
	switch entry.Type {
	case "gitlab":
		sm.client = NewGitLabClient(*entry)
	case "gitee":
		sm.client = NewGiteeClient(*entry)
	default:
		sm.client = NewGitHubClient(*entry, githubCfg)
	}
	return sm
}

// stop 停止同步循环
func (s *SyncManager) stop() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}

// loop 主循环：立即执行一次，之后按 interval 轮询；失败指数退避 3s → 15s → 60s → 5min。
func (s *SyncManager) loop(ctx context.Context) {
	interval := s.cfg.Interval
	if interval <= 0 {
		interval = 60 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	backoff := time.Duration(0)
	first := true
	for {
		if !first {
			select {
			case <-ctx.Done():
				logger.LogPrintf("🛑 [sync] 同步已停止")
				return
			case <-ticker.C:
			}
		}
		first = false
		if err := s.syncOnce(ctx); err != nil {
			if backoff == 0 {
				backoff = 3 * time.Second
			} else {
				switch backoff {
				case 3 * time.Second:
					backoff = 15 * time.Second
				case 15 * time.Second:
					backoff = 60 * time.Second
				default:
					backoff = 5 * time.Minute
				}
			}
			logger.LogPrintf("⚠️ [sync] 同步失败: %v（%.0fs 后重试）", err, backoff.Seconds())
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			continue
		}
		backoff = 0
	}
}

// syncOnce 执行一次完整同步（见 doc/sync-dev.md §6.2）。
func (s *SyncManager) syncOnce(ctx context.Context) error {
	cfg := s.cfg

	// 读本地 manifest
	manifest := LoadManifest(s.localRoot)

	// 首次同步（无 manifest）或增量树拉取失败（如未认证 API 限流）→
	// 直接用整仓归档（公开仓库走 codeload 不占 API 限额）+ 本地计算 git blob sha，
	// 完全不依赖 tree API，避免逐文件拉取触发限流。
	if len(manifest.Files) == 0 {
		if err := s.syncFromArchive(ctx, manifest); err != nil {
			// 归档不可用（如平台无归档/私有权限不足）→ 回退增量式首次全量（tree + 逐文件）
			logger.LogPrintf("⚠️ [sync] %s 归档同步失败(%v)，回退增量式首次全量", syncLabel(cfg), err)
		} else {
			return nil
		}
	}

	// 1. 拉取远端目录树（增量对比）
	remote, err := s.client.Tree(cfg.Branch, cfg.RepoPath)
	if err != nil {
		logger.LogPrintf("⚠️ [sync] %s 增量树拉取失败(%v)，降级整仓归档", syncLabel(cfg), err)
		return s.syncFromArchive(ctx, manifest)
	}
	// 2. 过滤 only_php + protect 保护清单（永不覆盖、永不删除）
	remoteFiles := map[string]string{}
	protected := 0
	for _, n := range remote {
		if cfg.OnlyPHP && !isPHPFile(n.Path) {
			continue
		}
		if isProtected(cfg.Protect, n.Path) {
			protected++
			continue
		}
		remoteFiles[n.Path] = n.SHA
	}

	// 4. 计算差异
	var toUpdate, toDelete []string
	for path, sha := range remoteFiles {
		if manifest.Files[path] != sha {
			toUpdate = append(toUpdate, path)
		}
	}
	if cfg.Delete != nil && *cfg.Delete {
		for path := range manifest.Files {
			if _, ok := remoteFiles[path]; !ok {
				if isProtected(cfg.Protect, path) {
					protected++
					continue
				}
				toDelete = append(toDelete, path)
			}
		}
	}

	// 4.5 决定拉取策略：变更多（含首次全量）时用归档包一次拉全仓，
	// 避免大仓库逐文件 blob 请求触发 GitHub 未认证 API 限流（60 次/小时）
	useArchive := len(toUpdate) > archiveThreshold
	var archiveFiles map[string][]byte
	if useArchive {
		data, err := s.client.Archive(cfg.Branch)
		if err != nil {
			logger.LogPrintf("⚠️ [sync] %s 下载仓库归档失败，回退逐文件: %v", syncLabel(cfg), err)
		} else if af, err := extractArchive(data); err != nil {
			logger.LogPrintf("⚠️ [sync] %s 解析归档失败，回退逐文件: %v", syncLabel(cfg), err)
		} else {
			archiveFiles = af
			logger.LogPrintf("ℹ️ [sync] %s 变更 %d 个文件，使用仓库归档（%d 个文件）", syncLabel(cfg), len(toUpdate), len(af))
		}
	}

	// 5. 应用更新
	result := &ApplyResult{}
	for _, path := range toUpdate {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		var content []byte
		if archiveFiles != nil {
			content = archiveFiles[path]
			if content == nil {
				logger.LogPrintf("⚠️ [sync] %s 归档中缺少 %s", syncLabel(cfg), path)
				result.Errors = append(result.Errors, fmt.Sprintf("归档缺少 %s", path))
				continue
			}
		} else {
			var err error
			content, err = s.client.Fetch(path, remoteFiles[path])
			if err != nil {
				logger.LogPrintf("⚠️ [sync] 下载失败 %s: %v", path, err)
				result.Errors = append(result.Errors, fmt.Sprintf("下载失败 %s: %v", path, err))
				continue
			}
		}
		if err := ApplyUpdate(s.localRoot, cfg, path, content, result); err != nil {
			continue // 已记日志
		}
		manifest.Files[path] = remoteFiles[path]
	}

	// 6. 删除远端已删文件（protect 保护内已在上方剔除）
	for _, path := range toDelete {
		if err := ApplyDelete(s.localRoot, cfg, path, result); err != nil {
			continue
		}
		delete(manifest.Files, path)
	}

	// 6.5 统计孤立文件（本地有、远端无，且不在 protect 内）→ 记日志供核对
	isolated := s.isolatedFiles(remoteFiles)

	// 7. 更新 manifest
	if err := manifest.Save(s.localRoot, s.client.RepoID(), cfg.Branch); err != nil {
		logger.LogPrintf("⚠️ [sync] 保存 manifest 失败: %v", err)
	}

	// 8. 日志
	skipped := len(remoteFiles) - len(toUpdate)
	logger.LogPrintf("📦 [sync] %s 增量完成: %d 更新 / %d 新增 / %d 删除 / %d 跳过 / %d 保护 / %d 孤立文件",
		syncLabel(cfg),
		result.Updated, result.Added, result.Deleted, skipped, protected, len(isolated))
	for _, p := range isolated {
		logger.LogPrintf("ℹ️ [sync]   孤立文件(本地私有，未同步): %s", p)
	}
	if len(result.Errors) > 0 {
		logger.LogPrintf("⚠️ [sync] %d 个文件处理失败: %v", len(result.Errors), result.Errors)
	}
	return nil
}

// syncFromArchive 整仓归档同步：下载 tar.gz → 解析 → 本地计算 git blob sha → 与 manifest 对比增量应用。
// 公开仓库走 codeload 直连不占 API 限额；首次同步/树 API 限流时使用，不依赖 tree API。
func (s *SyncManager) syncFromArchive(ctx context.Context, manifest *Manifest) error {
	cfg := s.cfg
	start := time.Now()

	data, err := s.client.Archive(cfg.Branch)
	if err != nil {
		return fmt.Errorf("下载仓库归档失败: %w", err)
	}
	files, err := extractArchive(data)
	if err != nil {
		return fmt.Errorf("解析仓库归档失败: %w", err)
	}

	// 构建远端文件表（本地计算 git blob sha，无需 tree API）
	remoteFiles := map[string]string{}
	protected := 0
	for path, content := range files {
		if cfg.OnlyPHP && !isPHPFile(path) {
			continue
		}
		if isProtected(cfg.Protect, path) {
			protected++
			continue
		}
		remoteFiles[path] = computeGitBlobSHA(content)
	}

	// 计算差异
	var toUpdate, toDelete []string
	for path, sha := range remoteFiles {
		if manifest.Files[path] != sha {
			toUpdate = append(toUpdate, path)
		}
	}
	if cfg.Delete != nil && *cfg.Delete {
		for path := range manifest.Files {
			if _, ok := remoteFiles[path]; !ok {
				if isProtected(cfg.Protect, path) {
					protected++
					continue
				}
				toDelete = append(toDelete, path)
			}
		}
	}

	// 应用更新（内容来自归档）
	result := &ApplyResult{}
	for _, path := range toUpdate {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := ApplyUpdate(s.localRoot, cfg, path, files[path], result); err != nil {
			continue // 已记日志
		}
		manifest.Files[path] = remoteFiles[path]
	}

	// 删除远端已删文件
	for _, path := range toDelete {
		if err := ApplyDelete(s.localRoot, cfg, path, result); err != nil {
			continue
		}
		delete(manifest.Files, path)
	}

	// 孤立文件报告
	isolated := s.isolatedFiles(remoteFiles)

	// 保存 manifest
	if err := manifest.Save(s.localRoot, s.client.RepoID(), cfg.Branch); err != nil {
		logger.LogPrintf("⚠️ [sync] 保存 manifest 失败: %v", err)
	}

	skipped := len(remoteFiles) - len(toUpdate)
	logger.LogPrintf("📦 [sync] %s 归档同步完成(%s): %d 更新 / %d 新增 / %d 删除 / %d 跳过 / %d 保护 / %d 孤立文件",
		syncLabel(cfg), time.Since(start).Round(time.Millisecond),
		result.Updated, result.Added, result.Deleted, skipped, protected, len(isolated))
	for _, p := range isolated {
		logger.LogPrintf("ℹ️ [sync]   孤立文件(本地私有，未同步): %s", p)
	}
	if len(result.Errors) > 0 {
		logger.LogPrintf("⚠️ [sync] %d 个文件处理失败: %v", len(result.Errors), result.Errors)
	}
	return nil
}

// computeGitBlobSHA 计算与 GitHub/GitLab blob sha 一致的本地校验值：
// sha1("blob " + 长度 + "\0" + 内容)，与 git 对象格式一致，可跨模式对比。
func computeGitBlobSHA(content []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(content))
	h.Write(content)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// isolatedFiles 枚举本地 localRoot 下的文件，返回远端树中不存在的本地私有文件（仅用于报告）。
func (s *SyncManager) isolatedFiles(remoteFiles map[string]string) []string {
	var isolated []string
	_ = filepath.WalkDir(s.localRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || path == s.localRoot {
			return nil
		}
		name := d.Name()
		// 跳过 manifest / 隐藏文件 / .bak 备份
		if name == manifestName || strings.HasPrefix(name, ".") || strings.Contains(name, ".bak.") {
			return nil
		}
		rel, err := filepath.Rel(s.localRoot, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if isProtected(s.cfg.Protect, rel) {
			return nil
		}
		if _, ok := remoteFiles[rel]; !ok {
			isolated = append(isolated, rel)
		}
		return nil
	})
	return isolated
}

// extractArchive 解析仓库归档，返回 相对路径(去掉顶层仓库目录前缀) → 内容。
// 自动识别 tar.gz（gzip 魔数）与 zip（PK 魔数），分别对应 GitHub/GitLab 与 Gitee 归档。
func extractArchive(data []byte) (map[string][]byte, error) {
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		return extractTarGz(data)
	}
	if len(data) >= 4 && data[0] == 'P' && data[1] == 'K' {
		return extractZip(data)
	}
	return nil, fmt.Errorf("无法识别的归档格式")
}

// extractTarGz 解析 tar.gz（GitHub / GitLab 归档）
func extractTarGz(data []byte) (map[string][]byte, error) {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	files := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		rel, ok := stripArchivePath(hdr.Name)
		if !ok {
			continue
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		files[rel] = content
	}
	return files, nil
}

// extractZip 解析 zip 归档（Gitee 归档）
func extractZip(data []byte) (map[string][]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rel, ok := stripArchivePath(f.Name)
		if !ok {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		files[rel] = content
	}
	return files, nil
}

// stripArchivePath 去掉顶层仓库目录（如 qist-tvbox-<sha>/），并做路径穿越防护。
func stripArchivePath(name string) (string, bool) {
	parts := strings.SplitN(filepath.ToSlash(name), "/", 2)
	if len(parts) != 2 || parts[1] == "" {
		return "", false
	}
	rel := parts[1]
	if filepath.IsAbs(rel) || strings.Contains(rel, "..") {
		return "", false // 防归档路径穿越
	}
	return rel, true
}
