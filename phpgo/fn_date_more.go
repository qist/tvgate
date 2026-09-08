package phpgo

// 日期时间扩展：idate / date_parse / date_parse_from_format / localtime。

import (
	"strings"
	"time"
)

// phpDateFromFormatLayout 把 PHP date 格式串转成 Go time.Parse 布局（用于 date_parse_from_format）。
func phpDateFromFormatLayout(format string) (string, []byte) {
	var b strings.Builder
	present := make([]byte, 0, 8) // 标记出现的组件：Y,m,d,H,i,s,M,F,D,l
	seen := func(name byte) {
		for _, x := range present {
			if x == name {
				return
			}
		}
		present = append(present, name)
	}
	for i := 0; i < len(format); i++ {
		c := format[i]
		switch c {
		case '\\':
			// PHP 转义：下一字符作为字面量
			if i+1 < len(format) {
				b.WriteByte(format[i+1])
				i++
			}
		case 'Y':
			b.WriteString("2006")
			seen('Y')
		case 'y':
			b.WriteString("06")
			seen('Y')
		case 'm', 'n':
			if c == 'm' {
				b.WriteString("01")
			} else {
				b.WriteString("1")
			}
			seen('m')
		case 'd', 'j':
			if c == 'd' {
				b.WriteString("02")
			} else {
				b.WriteString("2")
			}
			seen('d')
		case 'H', 'G':
			if c == 'H' {
				b.WriteString("15")
			} else {
				b.WriteString("15")
			}
			seen('H')
		case 'h', 'g':
			if c == 'h' {
				b.WriteString("03")
			} else {
				b.WriteString("3")
			}
			seen('H')
		case 'i':
			b.WriteString("04")
			seen('i')
		case 's':
			b.WriteString("05")
			seen('s')
		case 'A':
			b.WriteString("PM")
		case 'a':
			b.WriteString("pm")
		case 'M':
			b.WriteString("Jan")
			seen('M')
		case 'F':
			b.WriteString("January")
			seen('F')
		case 'D':
			b.WriteString("Mon")
			seen('D')
		case 'l':
			b.WriteString("Monday")
			seen('l')
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), present
}

// phpDateParts 组装 date_parse 风格的输出数组
func phpDateParts(t time.Time, present []byte, loc *time.Location) Value {
	arr := NewArray()
	has := func(name byte) bool {
		for _, x := range present {
			if x == name {
				return true
			}
		}
		return false
	}
	if has('Y') || len(present) == 0 {
		arr.ArraySet(NewString("year"), NewInt(int64(t.Year())))
	}
	if has('m') {
		arr.ArraySet(NewString("month"), NewInt(int64(t.Month())))
	}
	if has('d') {
		arr.ArraySet(NewString("day"), NewInt(int64(t.Day())))
	}
	if has('H') {
		arr.ArraySet(NewString("hour"), NewInt(int64(t.Hour())))
	}
	if has('i') {
		arr.ArraySet(NewString("minute"), NewInt(int64(t.Minute())))
	}
	if has('s') {
		arr.ArraySet(NewString("second"), NewInt(int64(t.Second())))
	}
	arr.ArraySet(NewString("fraction"), NewFloat(0))
	arr.ArraySet(NewString("warning_count"), NewInt(0))
	arr.ArraySet(NewString("warnings"), NewArray())
	arr.ArraySet(NewString("error_count"), NewInt(0))
	arr.ArraySet(NewString("errors"), NewArray())
	arr.ArraySet(NewString("is_localtime"), NewBool(loc != time.UTC))
	return arr
}

// phpDateParseError 返回解析失败的 date_parse 数组
func phpDateParseError() Value {
	arr := NewArray()
	errs := NewArray()
	errs.ArraySet(NewInt(0), NewString("The parsing did not succeed"))
	arr.ArraySet(NewString("error_count"), NewInt(1))
	arr.ArraySet(NewString("errors"), errs)
	arr.ArraySet(NewString("warning_count"), NewInt(1))
	warns := NewArray()
	warns.ArraySet(NewInt(0), NewString("The parsing did not succeed"))
	arr.ArraySet(NewString("warnings"), warns)
	arr.ArraySet(NewString("is_localtime"), NewBool(false))
	return arr
}

func init() {
	// idate($format_chr[, $timestamp])：返回单个日期分量的整数
	builtins["idate"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		ts := time.Now().Unix()
		if len(a) >= 2 {
			ts = a[1].ToInt()
		}
		loc := effectiveLoc(e)
		f := a[0].ToString()
		if f == "" {
			return NewBool(false), nil
		}
		out := phpDateIn(f[:1], ts, loc)
		return NewInt(parseLeadingNumber(out)), nil
	}
	builtins["date_parse"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		loc := effectiveLoc(e)
		t, ok := phpStrToTime(a[0].ToString(), loc)
		if !ok {
			return phpDateParseError(), nil
		}
		// date_parse 对完整/部分日期均补齐 0 分量
		return phpDateParts(t, nil, loc), nil
	}
	builtins["date_parse_from_format"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		layout, present := phpDateFromFormatLayout(a[0].ToString())
		loc := effectiveLoc(e)
		t, err := time.ParseInLocation(layout, a[1].ToString(), loc)
		if err != nil {
			return phpDateParseError(), nil
		}
		return phpDateParts(t, present, loc), nil
	}
	// localtime([$timestamp[, $associative = false]])：返回 tm_* 结构
	builtins["localtime"] = func(e *Env, a []Value) (Value, error) {
		ts := time.Now().Unix()
		if len(a) >= 1 {
			ts = a[0].ToInt()
		}
		assoc := false
		if len(a) >= 2 {
			assoc = a[1].ToBool()
		}
		loc := effectiveLoc(e)
		t := time.Unix(ts, 0).In(loc)
		vals := []int64{
			int64(t.Second()), int64(t.Minute()), int64(t.Hour()), int64(t.Day()),
			int64(t.Month()) - 1, int64(t.Year() - 1900), int64(t.Weekday()), int64(t.YearDay() - 1),
			0, // isdst
		}
		names := []string{"tm_sec", "tm_min", "tm_hour", "tm_mday", "tm_mon", "tm_year", "tm_wday", "tm_yday", "tm_isdst"}
		out := NewArray()
		if assoc {
			for i, n := range names {
				out.ArraySet(NewString(n), NewInt(vals[i]))
			}
		} else {
			for i, n := range names {
				_ = n
				out.ArraySet(NewInt(int64(i)), NewInt(vals[i]))
			}
		}
		return out, nil
	}
}
