package clear

import (
	"testing"
	"time"

	"github.com/qist/tvgate/config"
)

func TestClearAccessCacheByGroup(t *testing.T) {
	groupA := &config.ProxyGroupConfig{Proxies: []*config.ProxyConfig{{Name: "组A"}}}
	groupB := &config.ProxyGroupConfig{Proxies: []*config.ProxyConfig{{Name: "组B"}}}
	groupGone := &config.ProxyGroupConfig{Proxies: []*config.ProxyConfig{{Name: "已删组"}}}

	config.AccessCache.Lock()
	config.AccessCache.Mapping = map[string]*config.CachedGroup{
		"a.example.com|http://a": {Group: groupA},
		"b.example.com|http://b": {Group: groupB},
		"c.example.com|http://c": {Group: groupGone},
	}
	config.AccessCache.Unlock()
	t.Cleanup(func() {
		config.AccessCache.Lock()
		config.AccessCache.Mapping = map[string]*config.CachedGroup{}
		config.AccessCache.Unlock()
	})

	// 按组名精确清理，各条目互不影响；已删组（不在当前配置里）的残留也能清掉
	if n := ClearAccessCacheByGroup("组A"); n != 1 {
		t.Fatalf("组A 应清理 1 条, 实际 %d", n)
	}
	if n := ClearAccessCacheByGroup("已删组"); n != 1 {
		t.Fatalf("已删组 应清理 1 条, 实际 %d", n)
	}
	if n := ClearAccessCacheByGroup("组B"); n != 1 {
		t.Fatalf("组B 应清理 1 条, 实际 %d", n)
	}
	if n := ClearAccessCacheByGroup("组B"); n != 0 {
		t.Fatalf("重复清理应为 0 条, 实际 %d", n)
	}
	if n := ClearAccessCacheByGroup(""); n != 0 {
		t.Fatalf("空组名应清理 0 条, 实际 %d", n)
	}

	config.AccessCache.RLock()
	remain := len(config.AccessCache.Mapping)
	config.AccessCache.RUnlock()
	if remain != 0 {
		t.Fatalf("全部清理后应剩 0 条, 实际 %d", remain)
	}
}

func TestResetProxyGroupStats(t *testing.T) {
	group := &config.ProxyGroupConfig{
		Stats: &config.GroupStats{
			LastCheck: time.Now(),
			ProxyStats: map[string]*config.ProxyStats{
				"p1": {
					Alive:        true,
					ResponseTime: 100 * time.Millisecond,
					LastCheck:    time.Now(),
					FailCount:    3,
					StatusCode:   200,
					CooldownUntil: time.Now().Add(time.Minute),
				},
			},
		},
	}
	ResetProxyGroupStats(group)

	st := group.Stats.ProxyStats["p1"]
	if st.Alive || st.ResponseTime != 0 || st.FailCount != 0 || st.StatusCode != 0 {
		t.Fatalf("代理统计未重置: %+v", st)
	}
	if !st.LastCheck.IsZero() || !st.CooldownUntil.IsZero() {
		t.Fatalf("LastCheck/CooldownUntil 应清零: %+v", st)
	}
	if !group.Stats.LastCheck.IsZero() {
		t.Fatalf("组 LastCheck 应清零")
	}

	// nil / 无统计的组不应 panic
	ResetProxyGroupStats(nil)
	ResetProxyGroupStats(&config.ProxyGroupConfig{})
}
