package phpgo

import (
	"strconv"
	"sync"
	"time"
	_ "time/tzdata" // 内嵌 IANA 时区库，保证精简镜像(无 /usr/share/zoneinfo)也能 LoadLocation
)

// 全局默认时区（Env 创建时的播种值，缺省 UTC；脚本可用 date_default_timezone_set 修改）。
// 每次请求的 Env 各自持有 loc，互不影响，故此处仅静态读默认值，用互斥锁保护。
var (
	phpTimeLoc   *time.Location = time.UTC
	phpTimeLocMu sync.RWMutex
)

// currentPHPLocation 返回当前默认时区（date() 等按此输出）。
func currentPHPLocation() *time.Location {
	phpTimeLocMu.RLock()
	defer phpTimeLocMu.RUnlock()
	return phpTimeLoc
}

func init() {
	builtins["date"] = func(e *Env, a []Value) (Value, error) {
		format := a[0].ToString()
		ts := time.Now().Unix()
		if len(a) >= 2 {
			ts = a[1].ToInt()
		}
		return NewString(phpDateIn(format, ts, e.loc)), nil
	}
	builtins["gmdate"] = func(e *Env, a []Value) (Value, error) {
		format := a[0].ToString()
		ts := time.Now().Unix()
		if len(a) >= 2 {
			ts = a[1].ToInt()
		}
		return NewString(phpDateIn(format, ts, time.UTC)), nil
	}
	builtins["time"] = func(e *Env, a []Value) (Value, error) {
		return NewInt(time.Now().Unix()), nil
	}
	builtins["microtime"] = func(e *Env, a []Value) (Value, error) {
		now := time.Now()
		if len(a) >= 1 && a[0].ToBool() {
			return NewFloat(float64(now.UnixNano()) / 1e9), nil
		}
		sec := float64(now.UnixNano()%1e9) / 1e9
		return NewString(strconv.FormatFloat(sec, 'f', 8, 64) + " " + strconv.FormatInt(now.Unix(), 10)), nil
	}
	builtins["strtotime"] = func(e *Env, a []Value) (Value, error) {
		// 简化：尝试解析常见格式（无时区按当前请求默认时区解析）
		s := a[0].ToString()
		loc := e.loc
		if loc == nil {
			loc = time.UTC
		}
		naiveLayouts := []string{
			"2006-01-02 15:04:05",
			"2006-01-02 15:04",
			"2006-01-02",
			"20060102", // Ymd
			"20060102150405",
		}
		for _, layout := range naiveLayouts {
			if t, err := time.ParseInLocation(layout, s, loc); err == nil {
				return NewInt(t.Unix()), nil
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
				return NewInt(t.Unix()), nil
			}
		}
		// Ymd 格式：8位纯数字（如 20260828）
		if len(s) == 8 {
			if t, err := time.ParseInLocation("20060102", s, loc); err == nil {
				return NewInt(t.Unix()), nil
			}
		}
		// YmdHis 格式：14位纯数字
		if len(s) == 14 {
			if t, err := time.ParseInLocation("20060102150405", s, loc); err == nil {
				return NewInt(t.Unix()), nil
			}
		}
		return NewBool(false), nil
	}
	builtins["date_default_timezone_set"] = func(e *Env, a []Value) (Value, error) {
		loc, err := time.LoadLocation(a[0].ToString())
		if err != nil {
			return NewBool(false), nil
		}
		e.loc = loc
		return NewBool(true), nil
	}
	builtins["date_default_timezone_get"] = func(e *Env, a []Value) (Value, error) {
		if e.loc == nil {
			return NewString("UTC"), nil
		}
		return NewString(e.loc.String()), nil
	}
}

func phpDateIn(format string, ts int64, loc *time.Location) string {
	t := time.Unix(ts, 0).In(loc)
	var b []byte
	for i := 0; i < len(format); i++ {
		c := format[i]
		switch c {
		case 'Y':
			b = append(b, []byte(strconv.Itoa(t.Year()))...)
		case 'y':
			s := strconv.Itoa(t.Year())
			if len(s) >= 4 {
				b = append(b, []byte(s[2:])...)
			}
		case 'm':
			b = append(b, []byte(pad2(int(t.Month())))...)
		case 'n':
			b = append(b, []byte(strconv.Itoa(int(t.Month())))...)
		case 'd':
			b = append(b, []byte(pad2(t.Day()))...)
		case 'j':
			b = append(b, []byte(strconv.Itoa(t.Day()))...)
		case 'H':
			b = append(b, []byte(pad2(t.Hour()))...)
		case 'G':
			b = append(b, []byte(strconv.Itoa(t.Hour()))...)
		case 'i':
			b = append(b, []byte(pad2(t.Minute()))...)
		case 's':
			b = append(b, []byte(pad2(t.Second()))...)
		case 'D':
			b = append(b, []byte(t.Format("Mon"))...)
		case 'M':
			b = append(b, []byte(t.Format("Jan"))...)
		case 'F':
			b = append(b, []byte(t.Format("January"))...)
		case 'N':
			// ISO-8601 1=Mon..7=Sun
			w := int(t.Weekday())
			if w == 0 {
				w = 7
			}
			b = append(b, []byte(strconv.Itoa(w))...)
		case 'w':
			b = append(b, []byte(strconv.Itoa(int(t.Weekday())))...)
		case 'U':
			b = append(b, []byte(strconv.FormatInt(ts, 10))...)
		case 'v':
			b = append(b, []byte(pad3(t.Nanosecond()/1000))...)
		case 'u':
			b = append(b, []byte(pad3(t.Nanosecond()/1000))...)
		case '\\':
			if i+1 < len(format) {
				b = append(b, format[i+1])
				i++
			}
		default:
			b = append(b, c)
		}
	}
	return string(b)
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

func pad3(n int) string {
	s := strconv.Itoa(n)
	for len(s) < 3 {
		s = "0" + s
	}
	return s
}
