package player

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/qist/tvgate/config"
)

// resetLogoProbeState 清空包级探测缓存（测试间隔离）。
func resetLogoProbeState(t *testing.T) {
	t.Helper()
	logoProbeCache.Range(func(k, _ any) bool {
		logoProbeCache.Delete(k)
		return true
	})
	t.Cleanup(func() {
		logoProbeCache.Range(func(k, _ any) bool {
			logoProbeCache.Delete(k)
			return true
		})
	})
}

// setTestConfigPath 把 ConfigFilePath 指到临时目录（探测缓存落盘位置），测试后还原。
func setTestConfigPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := *config.ConfigFilePath
	*config.ConfigFilePath = filepath.Join(dir, "config.yaml")
	t.Cleanup(func() { *config.ConfigFilePath = old })
	return dir
}

// TestLogoProbeCachePersist 探测结论落盘 + 恢复：进程重启（APK 重启）后死链
// 不再重新探测、也不会随重启闪回前端 404。
func TestLogoProbeCachePersist(t *testing.T) {
	setTestConfigPath(t)
	resetLogoProbeState(t)

	logoProbeCache.Store("http://x/dead.png", logoProbeEntry{Exists: false, At: time.Now()})
	logoProbeCache.Store("http://x/stale.png", logoProbeEntry{Exists: true, At: time.Now().Add(-48 * time.Hour)})
	saveLogoProbeCache()

	// 模拟重启：内存清零后从磁盘恢复
	logoProbeCache.Delete("http://x/dead.png")
	logoProbeCache.Delete("http://x/stale.png")
	restoreLogoProbeCache(logoProbeCachePath())

	if e, ok := logoProbeCache.Load("http://x/dead.png"); !ok || e.(logoProbeEntry).Exists {
		t.Fatal("死链结论未恢复")
	}
	// 过期条目（超过最长 TTL 24h）不得恢复
	if _, ok := logoProbeCache.Load("http://x/stale.png"); ok {
		t.Fatal("过期条目不应恢复")
	}
}

// TestPruneDeadLogosSyncCacheApply 缓存命中（含死链）同步应用：聚合前语义保持，
// 且全部命中时不发起后台探测。
func TestPruneDeadLogosSyncCacheApply(t *testing.T) {
	setTestConfigPath(t)
	resetLogoProbeState(t)

	deadURL := "http://logo.example.com/dead.png"
	liveURL := "http://logo.example.com/live.png"
	logoProbeCache.Store(deadURL, logoProbeEntry{Exists: false, At: time.Now()})
	logoProbeCache.Store(liveURL, logoProbeEntry{Exists: true, At: time.Now()})

	chans := []*Channel{
		{Key: "a", Name: "A", TVGLogo: deadURL},
		{Key: "b", Name: "B", TVGLogo: liveURL},
		{Key: "c", Name: "C", TVGLogo: "/player/logo/C.png"}, // 本地路径不在探测范围
	}
	m := &Manager{}
	m.pruneDeadLogos(chans)

	if chans[0].TVGLogo != "" {
		t.Fatalf("缓存死链未被同步置空: %q", chans[0].TVGLogo)
	}
	if chans[1].TVGLogo != liveURL || chans[2].TVGLogo != "/player/logo/C.png" {
		t.Fatalf("存留台标被误清: %q %q", chans[1].TVGLogo, chans[2].TVGLogo)
	}
}

// TestProbeAndPruneClearsLive 后台探测轮：确认 404 的地址落盘并清掉当前在播
// 频道表里的死链（探测不阻塞重载，频道表先下发、死链随后清）。
func TestProbeAndPruneClearsLive(t *testing.T) {
	setTestConfigPath(t)
	resetLogoProbeState(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	deadURL := srv.URL + "/dead.png"

	m := &Manager{order: []*Channel{{Key: "a", Name: "A", TVGLogo: deadURL}}}
	m.probeAndPrune([]string{deadURL}, "")

	if got := m.order[0].TVGLogo; got != "" {
		t.Fatalf("后台探测后死链未清: %q", got)
	}
	// 结论已落盘：换一个空缓存实例恢复，应直接拿到死链结论
	if e, ok := logoProbeCache.Load(deadURL); !ok || e.(logoProbeEntry).Exists {
		t.Fatal("死链结论未落盘缓存")
	}
	logoProbeCache.Delete(deadURL)
	restoreLogoProbeCache(logoProbeCachePath())
	if _, ok := logoProbeCache.Load(deadURL); !ok {
		t.Fatal("落盘文件未能恢复死链结论")
	}
}
