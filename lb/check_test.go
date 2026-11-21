package lb

import (
	"testing"

	"github.com/qist/tvgate/config"
)

func TestGroupProbeUA(t *testing.T) {
	config.CfgMu.Lock()
	oldUA := config.Cfg.Player.UA
	config.CfgMu.Unlock()
	defer func() {
		config.CfgMu.Lock()
		config.Cfg.Player.UA = oldUA
		config.CfgMu.Unlock()
	}()

	setPlayerUA := func(ua string) {
		config.CfgMu.Lock()
		config.Cfg.Player.UA = ua
		config.CfgMu.Unlock()
	}

	setPlayerUA("player-ua")
	if got := GroupProbeUA(&config.ProxyGroupConfig{ProbeUA: "  group-ua  "}); got != "group-ua" {
		t.Fatalf("组级 probe_ua 优先失败: %q", got)
	}
	if got := GroupProbeUA(&config.ProxyGroupConfig{}); got != "player-ua" {
		t.Fatalf("回退 player.ua 失败: %q", got)
	}

	setPlayerUA("")
	if got := GroupProbeUA(&config.ProxyGroupConfig{}); got != "" {
		t.Fatalf("未配置时应返回空 UA: %q", got)
	}
	if got := GroupProbeUA(nil); got != "" {
		t.Fatalf("nil 组应返回空 UA: %q", got)
	}
}
