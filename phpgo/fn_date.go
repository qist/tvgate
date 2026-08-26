package phpgo

import (
	"strconv"
	"time"
)

func init() {
	builtins["date"] = func(e *Env, a []Value) (Value, error) {
		format := a[0].ToString()
		ts := time.Now().Unix()
		if len(a) >= 2 {
			ts = a[1].ToInt()
		}
		return NewString(phpDate(format, ts)), nil
	}
	builtins["gmdate"] = func(e *Env, a []Value) (Value, error) {
		format := a[0].ToString()
		ts := time.Now().Unix()
		if len(a) >= 2 {
			ts = a[1].ToInt()
		}
		return NewString(phpGmDate(format, ts)), nil
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
		// 简化：尝试解析常见格式
		s := a[0].ToString()
		layouts := []string{
			"2006-01-02 15:04:05",
			"2006-01-02T15:04:05Z",
			"2006-01-02",
			time.RFC3339,
			time.RFC1123,
		}
		for _, layout := range layouts {
			t, err := time.Parse(layout, s)
			if err == nil {
				return NewInt(t.Unix()), nil
			}
		}
		return NewBool(false), nil
	}
	builtins["date_default_timezone_set"] = func(e *Env, a []Value) (Value, error) {
		tz := a[0].ToString()
		loc, err := time.LoadLocation(tz)
		if err != nil {
			return NewBool(false), nil
		}
		_ = loc
		return NewBool(true), nil
	}
	builtins["date_default_timezone_get"] = func(e *Env, a []Value) (Value, error) {
		return NewString("UTC"), nil
	}
}

// phpDate 实现 PHP date() 格式化
func phpDate(format string, ts int64) string {
	t := time.Unix(ts, 0).Local()
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

// phpGmDate 实现 PHP gmdate() — UTC 版 date()
func phpGmDate(format string, ts int64) string {
	t := time.Unix(ts, 0).UTC()
	// 复用 phpDate 但用 UTC 时间
	return phpDateWithT(format, t)
}

func phpDateWithT(format string, t time.Time) string {
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
		case 'U':
			b = append(b, []byte(strconv.FormatInt(t.Unix(), 10))...)
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
