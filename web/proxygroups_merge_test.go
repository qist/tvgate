package web

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMergeUnknownGroupFieldsPreservesFutureKeys(t *testing.T) {
	const oldYAML = `
pool:
  servers: [192.0.2.1:1-2]
  future_opt: keep-me
proxies: []
`
	const newYAML = `
pool:
  servers: [192.0.2.2:3-4]
proxies: []
domains: [example.com]
`
	var oldNode, newNode yaml.Node
	if err := yaml.Unmarshal([]byte(oldYAML), &oldNode); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal([]byte(newYAML), &newNode); err != nil {
		t.Fatal(err)
	}
	oldMap := oldNode.Content[0]
	newMap := newNode.Content[0]

	mergeUnknownGroupFields(oldMap, newMap, nil)
	mergeUnknownGroupFields(mappingValue(oldMap, "pool"), mappingValue(newMap, "pool"), nil)

	if got := mappingValue(newMap, "domains"); got == nil {
		t.Fatal("本次提交的 domains 丢了")
	}
	pool := mappingValue(newMap, "pool")
	if pool == nil {
		t.Fatal("pool 丢了")
	}
	if got := mappingValue(pool, "future_opt"); got == nil || got.Value != "keep-me" {
		t.Fatalf("pool 内未识别字段未保留: %+v", got)
	}
}

// TestBuildProxyPoolNodeWritesAllFields 防止新增池参数后忘记在保存路径写 YAML：
// 后台编辑这些字段时能看到、能提交，但保存后不落地，重启就丢。
func TestBuildProxyPoolNodeWritesAllFields(t *testing.T) {
	poolMap := map[string]interface{}{
		"servers":             []interface{}{"192.0.2.1:1-2"},
		"protocol":            "socks5",
		"probe_enabled":       true,
		"probe_interval":      "10m",
		"probe_full_interval": "1h",
		"probe_url":           "http://example.com/live.m3u8",
		"probe_timeout":       "5s",
		"probe_retries":       float64(2),
		"probe_concurrency":   float64(16),
		"top_n":               float64(30),
		"fail_cooldown":       "60s",
		"max_ports":           float64(2048),
	}
	node := buildProxyPoolNode(poolMap)
	if node == nil {
		t.Fatal("buildProxyPoolNode 返回空节点")
	}

	if got := mappingValue(node, "servers"); got == nil || got.Kind != yaml.SequenceNode || len(got.Content) == 0 {
		t.Fatal("pool.servers 未写入")
	}
	for _, key := range []string{
		"protocol",
		"probe_enabled",
		"probe_interval",
		"probe_full_interval",
		"probe_url",
		"probe_timeout",
		"probe_retries",
		"probe_concurrency",
		"top_n",
		"fail_cooldown",
		"max_ports",
	} {
		if v := mappingValue(node, key); v == nil || v.Value == "" {
			t.Fatalf("pool.%s 未写入 YAML", key)
		}
	}
}
