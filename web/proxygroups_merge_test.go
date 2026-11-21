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
