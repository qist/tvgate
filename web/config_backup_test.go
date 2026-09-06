package web

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBackupConfigFile 验证统一配置备份的内容比对逻辑：
// 规则1：新内容与磁盘当前一致（没实际改动）→ 不备份；
// 规则2：磁盘当前状态与最近一份备份一致（刚备份过同状态）→ 不重复归档。
// 只对比磁盘配置与最近一份备份，不做全量历史扫描。
// 真实调用方流程：backupConfigFile 生成备份后，紧接着 os.WriteFile 写入新配置。
func TestBackupConfigFile(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	orig := []byte("a: 1\n")
	if err := os.WriteFile(cfg, orig, 0644); err != nil {
		t.Fatal(err)
	}

	backupCount := func() int {
		matches, _ := filepath.Glob(filepath.Join(dir, "config.yaml.backup.*"))
		return len(matches)
	}

	// 1. 内容与磁盘一致（点了保存但没改动）→ 不备份（规则1）
	if p, err := backupConfigFile(cfg, orig); err != nil || p != "" {
		t.Fatalf("unchanged content should skip backup, got path=%q err=%v", p, err)
	}
	if n := backupCount(); n != 0 {
		t.Fatalf("expected 0 backups, got %d", n)
	}

	// 2. A→B：生成备份（内容是改动前 A），随后写入 B
	vB := []byte("a: 2\n")
	p, err := backupConfigFile(cfg, vB)
	if err != nil || p == "" {
		t.Fatalf("changed content should create backup, got path=%q err=%v", p, err)
	}
	if b, _ := os.ReadFile(p); string(b) != string(orig) {
		t.Fatalf("backup should hold pre-change content, got %q", b)
	}
	if err := os.WriteFile(cfg, vB, 0644); err != nil {
		t.Fatal(err)
	}

	// 3. 磁盘已是 B，再次保存 B（无实际改动）→ 不备份（规则1）
	if p, err := backupConfigFile(cfg, vB); err != nil || p != "" {
		t.Fatalf("saving same content again should skip, got path=%q err=%v", p, err)
	}
	if n := backupCount(); n != 1 {
		t.Fatalf("expected 1 backup, got %d", n)
	}

	// 4. B→C：生成备份（内容是 B），不写入磁盘（模拟下一步保存前）
	vC := []byte("a: 3\n")
	p, err = backupConfigFile(cfg, vC)
	if err != nil || p == "" {
		t.Fatalf("new change should create backup, got path=%q err=%v", p, err)
	}
	if b, _ := os.ReadFile(p); string(b) != string(vB) {
		t.Fatalf("backup should hold B, got %q", b)
	}
	if n := backupCount(); n != 2 {
		t.Fatalf("expected 2 backups, got %d", n)
	}
	// 此时磁盘仍是 B，最近一份备份内容也是 B

	// 5. 再保存 D：磁盘状态 B 刚备份过 → 不重复归档（规则2）
	vD := []byte("a: 4\n")
	if p, err := backupConfigFile(cfg, vD); err != nil || p != "" {
		t.Fatalf("state identical to latest snapshot should skip, got path=%q err=%v", p, err)
	}
	if n := backupCount(); n != 2 {
		t.Fatalf("expected 2 backups, got %d", n)
	}
}
