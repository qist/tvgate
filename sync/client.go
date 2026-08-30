package sync

// FileNode 仓库树中的一个文件节点
type FileNode struct {
	Path string // 仓库内相对路径（已去掉 repo_path 前缀，相对 local_path 根）
	SHA  string // blob sha（GitHub）或 id（GitLab），作为变更依据
	Mode string // 类型（blob/tree）
}

// RepoClient 统一仓库访问接口
type RepoClient interface {
	// Tree 递归拉取目录树，按 prefix（repo_path）过滤并去掉前缀，返回目标子树的文件节点
	Tree(branch, prefix string) ([]FileNode, error)
	// Fetch 取文件内容；ref 为变更依据（GitHub 用 blob sha，GitLab 用分支名）
	Fetch(path, ref string) ([]byte, error)
	// Archive 下载整个仓库的 tar.gz 归档（1 次请求，避免大仓库逐文件拉取触发 API 限流）
	Archive(branch string) ([]byte, error)
	// RepoID 仓库标识（用于 manifest 记录源）
	RepoID() string
}
