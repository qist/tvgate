package player

import "testing"

func TestShiftSegmentHourDir(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "整点跨小时目录 → 回退上一小时（实测 13:00 的 178962117：13 点目录 404、12 点目录 200）",
			in:   "http://cdn.example.com/path/live/program/live/ch-demo/8000000/2026091713/178962117.ts",
			want: "http://cdn.example.com/path/live/program/live/ch-demo/8000000/2026091712/178962117.ts",
		},
		{
			name: "跨天（00 点 → 前一天 23 点）",
			in:   "http://x/a/2026091700/178962000.ts",
			want: "http://x/a/2026091623/178962000.ts",
		},
		{
			name: "保留 query",
			in:   "http://x/a/2026091713/178962117.ts?token=abc&x=1",
			want: "http://x/a/2026091712/178962117.ts?token=abc&x=1",
		},
		{
			name: "fMP4 分片（m4s）同样处理",
			in:   "http://x/a/2026091713/178962117.m4s",
			want: "http://x/a/2026091712/178962117.m4s",
		},
	}
	for _, c := range cases {
		got, ok := shiftSegmentHourDir(c.in, -1)
		if !ok || got != c.want {
			t.Fatalf("%s: shiftSegmentHourDir(%q) = %q, ok=%v; want %q", c.name, c.in, got, ok, c.want)
		}
	}
}

func TestShiftSegmentHourDirSkipsNonSegment(t *testing.T) {
	for _, in := range []string{
		"http://x/live/index.m3u8",
		"http://x/live/2026091713/index.m3u8",
		"http://x/live/program/live/ch-demo/8000000/2026091713/178962117.tsx",
		"http://x/live/program/live/ch-demo/8000000/2026091713/abc.ts",
		"http://x/live/program/live/ch-demo/8000000/202609171/178962117.ts",
		"http://x/live/program/live/ch-demo/8000000/178962117.ts",
	} {
		if got, ok := shiftSegmentHourDir(in, -1); ok {
			t.Fatalf("shiftSegmentHourDir(%q) 不应改写，却得到 %q", in, got)
		}
	}
}
