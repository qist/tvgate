package web

import (
	"strings"
	"testing"

	"github.com/qist/tvgate/config"
	"gopkg.in/yaml.v3"
)

// TestPlayerSourceList 多订阅源提交值归一化：数组/分隔字符串都接受，去空去空白，保持原序。
func TestPlayerSourceList(t *testing.T) {
	cases := []struct {
		name string
		in   interface{}
		want []string
	}{
		{"数组", []interface{}{" http://a/x.txt ", "", "http://b/y.txt"}, []string{"http://a/x.txt", "http://b/y.txt"}},
		{"字符串数组", []string{"http://c/z.txt"}, []string{"http://c/z.txt"}},
		{"换行逗号分号竖线", "http://d/1.txt\nhttp://d/2.txt,http://d/3.txt;http://d/4.txt|http://d/5.txt",
			[]string{"http://d/1.txt", "http://d/2.txt", "http://d/3.txt", "http://d/4.txt", "http://d/5.txt"}},
		{"空值", "", nil},
		{"非字符串标量", 12, []string{"12"}},
	}
	for _, c := range cases {
		got := playerSourceList(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("[%s] 数量不符: got=%v want=%v", c.name, got, c.want)
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Fatalf("[%s] 第 %d 项不符: got=%q want=%q", c.name, i, got[i], c.want[i])
			}
		}
	}
}

// TestBuildPlayerNodeSubscriptions 保存 player 段时写出 subscriptions 序列；未提交该键时不写。
func TestBuildPlayerNodeSubscriptions(t *testing.T) {
	want := []string{"http://127.0.0.1:8888/php/xxx.php?id=all", "http://x/y.txt"}
	node := buildPlayerNode(map[string]interface{}{
		"enabled":      true,
		"subscription": "/opt/tvgate/build/tv.txt",
		"subscriptions": []interface{}{
			want[0], " " + want[1] + " ",
		},
	})
	out, err := yaml.Marshal(node)
	if err != nil {
		t.Fatal(err)
	}
	// buildPlayerNode 产出的是 player 段内部节点，补一层键名后按结构体解析
	var parsed struct {
		Player struct {
			Subscription  string   `yaml:"subscription"`
			Subscriptions []string `yaml:"subscriptions"`
		} `yaml:"player"`
	}
	if err := yaml.Unmarshal([]byte("player:\n"+indentLines(out)), &parsed); err != nil {
		t.Fatalf("解析产物失败: %v", err)
	}
	if parsed.Player.Subscription != "/opt/tvgate/build/tv.txt" {
		t.Fatalf("subscription 不符: %q", parsed.Player.Subscription)
	}
	if len(parsed.Player.Subscriptions) != len(want) {
		t.Fatalf("subscriptions 不符: %v", parsed.Player.Subscriptions)
	}
	for i := range want {
		if parsed.Player.Subscriptions[i] != want[i] {
			t.Fatalf("subscriptions[%d] 不符: %q", i, parsed.Player.Subscriptions[i])
		}
	}

	// 未提交 subscriptions：不产出该键（避免把配置写成空列表）
	plain, err := yaml.Marshal(buildPlayerNode(map[string]interface{}{"enabled": true}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "subscriptions") {
		t.Fatalf("不该写出 subscriptions: %s", plain)
	}

	// 提交空数组（= 用户在页面上清空）：同样不产出该键 → 保存后由"提交里有没有该键"判定删除，
	// 不得回填旧值（否则页面上清空多订阅源不生效）。
	emptied, err := yaml.Marshal(buildPlayerNode(map[string]interface{}{"enabled": true, "subscriptions": []interface{}{}}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(emptied), "subscriptions") {
		t.Fatalf("空数组不该写出 subscriptions: %s", emptied)
	}
}

// TestBuildPlayerNodeEpgs 保存 player 段时写出 epgs 序列（多 EPG 来源，含 {name} 模板值）；
// 未提交该键 / 提交空数组时不写（页面清空要真的生效，不能被旧值回填）。
func TestBuildPlayerNodeEpgs(t *testing.T) {
	want := []string{"http://epg.51zmt.top:8000/e1.xml.gz", "https://epg.example.com/?ch={name}&date={date}"}
	node := buildPlayerNode(map[string]interface{}{
		"enabled": true,
		"epg":     "https://epg.cdn.loc.cc/?ch={name}&date={date}",
		"epgs":    []interface{}{want[0], " " + want[1] + " "},
	})
	out, err := yaml.Marshal(node)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Player config.PlayerConfig `yaml:"player"`
	}
	if err := yaml.Unmarshal([]byte("player:\n"+indentLines(out)), &parsed); err != nil {
		t.Fatalf("解析产物失败: %v", err)
	}
	if parsed.Player.Epg != "https://epg.cdn.loc.cc/?ch={name}&date={date}" {
		t.Fatalf("epg 不符: %q", parsed.Player.Epg)
	}
	if len(parsed.Player.Epgs) != len(want) {
		t.Fatalf("epgs 不符: %v", parsed.Player.Epgs)
	}
	for i := range want {
		if parsed.Player.Epgs[i] != want[i] {
			t.Fatalf("epgs[%d] 不符: %q", i, parsed.Player.Epgs[i])
		}
	}

	for _, c := range []struct {
		name string
		body map[string]interface{}
	}{
		{"未提交 epgs", map[string]interface{}{"enabled": true}},
		{"提交空数组", map[string]interface{}{"enabled": true, "epgs": []interface{}{}}},
	} {
		plain, err := yaml.Marshal(buildPlayerNode(c.body))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(plain), "epgs") {
			t.Fatalf("[%s] 不该写出 epgs: %s", c.name, plain)
		}
	}
}

func indentLines(b []byte) string {
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	for i := range lines {
		lines[i] = "  " + lines[i]
	}
	return strings.Join(lines, "\n") + "\n"
}
