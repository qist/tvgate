package tasks

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// expr 表示一个已解析的标准 5 段 cron 表达式（分 时 日 月 周）。
// 各字段用布尔位表加速匹配。
type expr struct {
	minute [60]bool
	hour   [24]bool
	dom    [31]bool // 每月的某一天 1-31
	month  [13]bool // 1-12
	dow    [8]bool  // 0-6（0/7 = 周日）
	// domRestricted / dowRestricted 标记日字段是否被显式约束（非 "*"）。
	// cron 语义：两者都受限时取 OR；仅一方受限时以该方为准。
	domRestricted bool
	dowRestricted bool
}

// maxNextTicks 限制向后搜索的最大分钟数（约 10 年），避免无效表达式死循环。
const maxNextTicks = 10 * 365 * 24 * 60

// parseCron 解析标准 5 字段 cron 表达式。
// 支持：* 、*/n 、n 、a-b 、a-b/n 、a,b,c 。月份/星期仅支持数字（不支持英文名）。
func parseCron(s string) (*expr, error) {
	fields := strings.Fields(strings.TrimSpace(s))
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron 表达式需为 5 段（分 时 日 月 周），当前 %d 段", len(fields))
	}
	e := &expr{}
	var err error
	if err = parseField(fields[0], 0, 59, e.minute[:]); err != nil {
		return nil, fmt.Errorf("分字段不合法: %w", err)
	}
	if err = parseField(fields[1], 0, 23, e.hour[:]); err != nil {
		return nil, fmt.Errorf("时字段不合法: %w", err)
	}
	if err = parseField(fields[2], 1, 31, e.dom[:]); err != nil {
		return nil, fmt.Errorf("日字段不合法: %w", err)
	} else {
		e.domRestricted = fields[2] != "*"
	}
	if err = parseField(fields[3], 1, 12, e.month[:]); err != nil {
		return nil, fmt.Errorf("月字段不合法: %w", err)
	}
	if fields[4] == "7" { // 允许 7 表示周日，归一化到下标 0
		fields[4] = "0"
	}
	if err = parseField(fields[4], 0, 6, e.dow[:]); err != nil {
		return nil, fmt.Errorf("周字段不合法: %w", err)
	} else {
		e.dowRestricted = fields[4] != "*"
	}
	return e, nil
}

// parseField 解析单个字段为布尔位表。
func parseField(field string, min, max int, set []bool) error {
	if field == "*" {
		for i := min; i <= max; i++ {
			if i < len(set) {
				set[i] = true
			}
		}
		return nil
	}
	for _, part := range strings.Split(field, ",") {
		if err := parseFieldPart(part, min, max, set); err != nil {
			return err
		}
	}
	return nil
}

// parseFieldPart 解析形如 "*/n"、"n"、"a-b"、"a-b/n" 的单个片段。
func parseFieldPart(part string, min, max int, set []bool) error {
	step := 1
	// 提取步长
	if idx := strings.IndexByte(part, '/'); idx >= 0 {
		sv, err := strconv.Atoi(part[idx+1:])
		if err != nil || sv <= 0 {
			return fmt.Errorf("非法步长 %q", part[idx+1:])
		}
		step = sv
		part = part[:idx]
	}

	lo, hi := min, max
	switch {
	case part == "*":
		lo, hi = min, max
	case strings.Contains(part, "-"):
		segs := strings.SplitN(part, "-", 2)
		if len(segs) != 2 {
			return fmt.Errorf("非法范围 %q", part)
		}
		a, err1 := strconv.Atoi(segs[0])
		b, err2 := strconv.Atoi(segs[1])
		if err1 != nil || err2 != nil {
			return fmt.Errorf("非法范围 %q", part)
		}
		lo, hi = a, b
	default:
		v, err := strconv.Atoi(part)
		if err != nil {
			return fmt.Errorf("非法数值 %q", part)
		}
		lo, hi = v, v
	}

	if lo < min || hi > max || lo > hi {
		return fmt.Errorf("数值超出范围 [%d-%d]: %q", min, max, part)
	}
	for i := lo; i <= hi; i += step {
		if i < len(set) {
			set[i] = true
		}
	}
	return nil
}

// dayMatches 判断时间 t 是否命中「日」约束。
func (e *expr) dayMatches(t time.Time) bool {
	dom := t.Day() >= 1 && t.Day() <= 31 && e.dom[t.Day()]
	dow := e.dow[int(t.Weekday())]
	switch {
	case e.domRestricted && e.dowRestricted:
		return dom || dow
	case e.domRestricted:
		return dom
	case e.dowRestricted:
		return dow
	default:
		return true
	}
}

// next 返回严格晚于 from 的下一次触发时间（位置本地时区）。
// 使用“逐分钟扫描 + 字段修剪”的方式：通常很快命中常见表达式，最多退让到 maxNextTicks。
func (e *expr) next(from time.Time) time.Time {
	t := from.Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < maxNextTicks; i++ {
		if e.month[t.Month()] && e.dayMatches(t) && e.hour[t.Hour()] && e.minute[t.Minute()] {
			return t
		}
		t = t.Add(time.Minute)
	}
	return time.Time{} // 未找到（表达式无解）
}
