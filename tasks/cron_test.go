package tasks

import (
	"testing"
	"time"
)

func TestParseCronAndNext(t *testing.T) {
	loc := time.Local
	cases := []struct {
		expr string
		from time.Time
		want string // "2006-01-02 15:04"
	}{
		{"0 2 * * *", time.Date(2026, 9, 2, 10, 0, 0, 0, loc), "2026-09-03 02:00"},
		{"0 2 * * *", time.Date(2026, 9, 2, 1, 59, 0, 0, loc), "2026-09-02 02:00"},
		{"*/30 * * * *", time.Date(2026, 9, 2, 10, 15, 0, 0, loc), "2026-09-02 10:30"},
		{"*/30 * * * *", time.Date(2026, 9, 2, 10, 30, 0, 0, loc), "2026-09-02 11:00"}, // 严格晚于
		{"0 9 * * 1-5", time.Date(2026, 9, 2, 8, 0, 0, 0, loc), "2026-09-02 09:00"},     // 周三
		{"0 9 * * 1-5", time.Date(2026, 9, 5, 10, 0, 0, 0, loc), "2026-09-07 09:00"},    // 周六→下周一
		{"0 8 1,15 * *", time.Date(2026, 9, 2, 0, 0, 0, 0, loc), "2026-09-15 08:00"},
		{"0 0 1 1 *", time.Date(2026, 2, 2, 0, 0, 0, 0, loc), "2027-01-01 00:00"}, // 每年元旦
	}
	for _, c := range cases {
		e, err := parseCron(c.expr)
		if err != nil {
			t.Fatalf("parseCron(%q) unexpected error: %v", c.expr, err)
		}
		got := e.next(c.from)
		if got.IsZero() {
			t.Errorf("parseCron(%q) next(%s) = zero time, want %s", c.expr, c.from, c.want)
			continue
		}
		if got.Format("2006-01-02 15:04") != c.want {
			t.Errorf("parseCron(%q) next(%s) = %s, want %s", c.expr, c.from, got.Format("2006-01-02 15:04"), c.want)
		}
	}
}

func TestParseCronInvalid(t *testing.T) {
	bad := []string{"", "* * * *", "a b c d e", "0 0 0 * *", "*/0 * * * *", "61 * * * *"}
	for _, s := range bad {
		if _, err := parseCron(s); err == nil {
			t.Errorf("parseCron(%q) expected error, got nil", s)
		}
	}
}