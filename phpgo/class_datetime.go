package phpgo

import (
	"strconv"
	"strings"
	"time"
)

// 内置 DateTime 类的最小实现。
//
// phpgo 没有真正的 DateTime 类（instanceof 走 eval.go 的特例），但大量
// IPTV 解析脚本（如 akmg.php 的 playseek 回看分支）依赖：
//
//	$d = new DateTime('YmdHis 字符串');
//	$d->modify('-8 hours');
//	$s = $d->format('YmdHis');
//
// 这里以「携带 __ts 属性的 KindObject」模拟 DateTime 实例：
//   - new DateTime($time)：$time 解析失败时对象不带 __ts，后续方法返回 false
//     （与真实 PHP 抛异常的差距可接受：脚本普遍不捕获，行为只需"不致命"）
//   - new DateTime()：等价 now
//   - 修改类方法（modify/setTimestamp）直接原地改 __ts 并返回自身
//
// 时区：统一用当前请求的 e.loc（wall-clock 语义），与 date()/strtotime 一致，
// 这样脚本里 "-8 hours" 之类的手工时区换算结果与真实 PHP 部署一致。

// phpStrToTime 按常见 PHP 日期格式解析时间字符串（naive 值按 loc 解释）。
func phpStrToTime(s string, loc *time.Location) (time.Time, bool) {
	naiveLayouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
		"20060102", // Ymd
		"20060102150405", // YmdHis
	}
	for _, layout := range naiveLayouts {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, true
		}
	}
	// 带时区/时区偏移的格式
	zoneLayouts := []string{
		"2006-01-02T15:04:05Z",
		time.RFC3339,
		time.RFC1123,
		time.RFC1123Z,
	}
	for _, layout := range zoneLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// phpDateTimeModify 解析 modify() 的相对时间表达式（如 "-8 hours"、"+1 day -2 hours"），
// 返回新的 Unix 时间戳。支持 成对出现的 数字+单位（sec/min/hour/day/week/month/year）。
func phpDateTimeModify(ts int64, expr string, loc *time.Location) (int64, bool) {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) == 0 || len(fields)%2 != 0 {
		return ts, false
	}
	t := time.Unix(ts, 0).In(loc)
	for i := 0; i+1 < len(fields); i += 2 {
		n, err := strconv.ParseInt(fields[i], 10, 64)
		if err != nil {
			return ts, false
		}
		// 去掉复数 s（hours → hour）
		unit := strings.TrimSuffix(strings.ToLower(fields[i+1]), "s")
		switch unit {
		case "sec", "second":
			t = t.Add(time.Duration(n) * time.Second)
		case "min", "minute":
			t = t.Add(time.Duration(n) * time.Minute)
		case "hour":
			t = t.Add(time.Duration(n) * time.Hour)
		case "day":
			t = t.AddDate(0, 0, int(n))
		case "week":
			t = t.AddDate(0, 0, int(n)*7)
		case "month":
			t = t.AddDate(0, int(n), 0)
		case "year":
			t = t.AddDate(int(n), 0, 0)
		default:
			return ts, false
		}
	}
	return t.Unix(), true
}

// dateTimeBuiltinMethod 分派内置 DateTime 对象的方法调用。
// 返回 handled=true 表示已处理（结果含错误时一并返回）。
func (e *Env) dateTimeBuiltinMethod(recv Value, method string, args []Expr) (Value, bool, error) {
	if !strings.EqualFold(recv.Object.ClassName, "DateTime") {
		return NewNull(), false, nil
	}
	vals := make([]Value, 0, len(args))
	for _, a := range args {
		v, err := e.evalExpr(a)
		if err != nil {
			return NewNull(), true, err
		}
		vals = append(vals, v)
	}
	tsv, ok := recv.Object.Properties["__ts"]
	if !ok {
		// 构造时解析失败：所有方法返回 false（接近 PHP fatal 的"安全降级"）
		return NewBool(false), true, nil
	}
	ts := tsv.ToInt()
	loc := e.loc
	if loc == nil {
		loc = time.UTC
	}
	switch strings.ToLower(method) {
	case "format":
		if len(vals) == 0 {
			return NewBool(false), true, nil
		}
		return NewString(phpDateIn(vals[0].ToString(), ts, loc)), true, nil
	case "modify":
		if len(vals) == 0 {
			return NewBool(false), true, nil
		}
		nt, ok2 := phpDateTimeModify(ts, vals[0].ToString(), loc)
		if !ok2 {
			return NewBool(false), true, nil
		}
		recv.Object.Properties["__ts"] = NewInt(nt)
		return recv, true, nil
	case "gettimestamp":
		return NewInt(ts), true, nil
	case "settimestamp":
		if len(vals) == 0 {
			return NewBool(false), true, nil
		}
		recv.Object.Properties["__ts"] = NewInt(vals[0].ToInt())
		return recv, true, nil
	}
	return NewNull(), true, nil
}
