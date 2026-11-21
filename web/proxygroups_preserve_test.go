package web

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/config/load"
	"github.com/qist/tvgate/pool"
)

// TestSaveProxyGroupsKeepsPoolRuntimeOfAllGroups 回归：后台保存代理组配置
// （即使只改了其中一组）后，所有池的可用节点与整个端口段的测速状态都必须保留，
// 不能因为热重载创建了新组对象就把池数据清空重测。
func TestSaveProxyGroupsKeepsPoolRuntimeOfAllGroups(t *testing.T) {
	const raw = `
log:
  enabled: false
web:
  enabled: false
reload: 5
proxygroups:
  g1:
    domains: [a.example]
    interval: 1m
    loadbalance: fastest
    pool:
      servers: ["192.0.2.1:8000-8004"]
      protocol: http
      probe_url: http://a.example/live.m3u8
      probe_interval: 1m
      probe_full_interval: 1h
  g2:
    domains: [b.example]
    interval: 1m
    loadbalance: fastest
    proxies:
      - name: static-1
        type: http
        server: 192.0.2.9
        port: 8080
    pool:
      servers: ["192.0.2.2:9000-9002"]
      protocol: socks5
      probe_url: http://b.example/live.m3u8
      probe_interval: 1m
      probe_full_interval: 1h
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}
	oldPath := config.ConfigFilePath
	config.ConfigFilePath = &path
	defer func() {
		config.ConfigFilePath = oldPath
		pool.StopAll()
	}()

	if err := load.LoadConfig(path); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	// 模拟探测结果：每组只保留 1 个可用节点，但整个端口段都留有测速历史。
	for _, name := range []string{"g1", "g2"} {
		pg := config.Cfg.ProxyGroups[name]
		nodes, err := pg.BuildPoolNodes()
		if err != nil || len(nodes) < 3 {
			t.Fatalf("%s BuildPoolNodes: %v, len=%d", name, err, len(nodes))
		}
		pg.SetPoolNodes(nodes[:1])
		pg.Stats.Lock()
		for i, n := range nodes {
			if i >= 3 {
				break
			}
			pg.Stats.ProxyStats[n.Name] = &config.ProxyStats{
				Alive:      i == 0,
				Throughput: int64(1 << 20),
				FailCount:  i,
			}
		}
		pg.Stats.Unlock()
	}

	// 按前端方式提交一次保存：GET 出配置，剥离运行态字段后整体 POST 回去。
	h := &ConfigHandler{}
	rec := httptest.NewRecorder()
	h.handleProxyGroupsConfig(rec, httptest.NewRequest("GET", "/", nil))
	var payload map[string]map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("GET json: %v body=%s", err, rec.Body.String())
	}
	for _, g := range payload {
		delete(g, "stats")
		delete(g, "pool_nodes")
	}
	body, _ := json.Marshal(payload)

	rec2 := httptest.NewRecorder()
	h.handleProxyGroupsConfigSave(rec2, httptest.NewRequest("POST", "/", bytes.NewReader(body)))
	if rec2.Code != 200 {
		t.Fatalf("保存失败 status=%d body=%s", rec2.Code, rec2.Body.String())
	}

	for _, name := range []string{"g1", "g2"} {
		pg := config.Cfg.ProxyGroups[name]
		nodes, _ := pg.BuildPoolNodes()
		if got := len(pg.PoolNodes()); got != 1 {
			t.Errorf("%s: 保存后可用节点丢失，got %d", name, got)
		}
		for i, n := range nodes {
			if i >= 3 {
				break
			}
			if _, ok := pg.Stats.ProxyStats[n.Name]; !ok {
				t.Errorf("%s: 保存后端口 %s 的测速状态丢失", name, n.Name)
			}
		}
	}
}
