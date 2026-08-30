package sync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Manifest 同步目标根下的同步记录（{localRoot}/.manifest.json）。
// 同步本身不把它当脚本。
type Manifest struct {
	Repo        string            `json:"repo"`
	Branch      string            `json:"branch"`
	GeneratedAt int64             `json:"generated_at"`
	Files       map[string]string `json:"files"` // relPath -> sha
}

const manifestName = ".manifest.json"

func manifestPath(localRoot string) string {
	return filepath.Join(localRoot, manifestName)
}

// LoadManifest 读取 manifest；不存在/损坏返回空（视为首次同步，全量对比后重建）。
func LoadManifest(localRoot string) *Manifest {
	m := &Manifest{Files: map[string]string{}}
	b, err := os.ReadFile(manifestPath(localRoot))
	if err != nil {
		return m
	}
	if err := json.Unmarshal(b, m); err != nil {
		return &Manifest{Files: map[string]string{}}
	}
	if m.Files == nil {
		m.Files = map[string]string{}
	}
	return m
}

// Save 写回 manifest（临时文件 + 原子替换）。
func (m *Manifest) Save(localRoot, repo, branch string) error {
	m.Repo = repo
	m.Branch = branch
	m.GeneratedAt = time.Now().Unix()
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	dst := manifestPath(localRoot)
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
