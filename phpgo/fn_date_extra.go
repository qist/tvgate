package phpgo

import (
	"time"
)

func init() {
	// mktime：本地时区构造时间戳；缺省参数取当前时间对应分量（PHP 语义）
	builtins["mktime"] = func(e *Env, a []Value) (Value, error) {
		return phpMktime(e, a, false), nil
	}
	builtins["gmmktime"] = func(e *Env, a []Value) (Value, error) {
		return phpMktime(e, a, true), nil
	}
	builtins["checkdate"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 3 {
			return NewBool(false), nil
		}
		month := int(a[0].ToInt())
		day := int(a[1].ToInt())
		year := int(a[2].ToInt())
		if month < 1 || month > 12 || day < 1 || day > 31 {
			return NewBool(false), nil
		}
		// 校验日是否合法（利用 Go 的日期归一化：构造后各分量应不变）
		t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
		return NewBool(t.Year() == year && int(t.Month()) == month && t.Day() == day), nil
	}
	builtins["getdate"] = func(e *Env, a []Value) (Value, error) {
		ts := time.Now().Unix()
		if len(a) >= 1 {
			ts = a[0].ToInt()
		}
		loc := e.loc
		if loc == nil {
			loc = time.UTC
		}
		t := time.Unix(ts, 0).In(loc)
		wday := int(t.Weekday())
		weekdays := []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
		months := []string{"January", "February", "March", "April", "May", "June",
			"July", "August", "September", "October", "November", "December"}
		arr := NewArray()
		arr.ArraySet(NewString("seconds"), NewInt(int64(t.Second())))
		arr.ArraySet(NewString("minutes"), NewInt(int64(t.Minute())))
		arr.ArraySet(NewString("hours"), NewInt(int64(t.Hour())))
		arr.ArraySet(NewString("mday"), NewInt(int64(t.Day())))
		arr.ArraySet(NewString("wday"), NewInt(int64(wday)))
		arr.ArraySet(NewString("mon"), NewInt(int64(t.Month())))
		arr.ArraySet(NewString("year"), NewInt(int64(t.Year())))
		arr.ArraySet(NewString("yday"), NewInt(int64(t.YearDay()-1)))
		arr.ArraySet(NewString("weekday"), NewString(weekdays[wday]))
		arr.ArraySet(NewString("month"), NewString(months[t.Month()-1]))
		arr.ArraySet(NewString("0"), NewInt(ts))
		return arr, nil
	}
	builtins["gettimeofday"] = func(e *Env, a []Value) (Value, error) {
		now := time.Now()
		arr := NewArray()
		arr.ArraySet(NewString("sec"), NewInt(now.Unix()))
		arr.ArraySet(NewString("usec"), NewInt(int64(now.Nanosecond()/1000)))
		arr.ArraySet(NewString("minuteswest"), NewInt(0))
		arr.ArraySet(NewString("dsttime"), NewInt(0))
		return arr, nil
	}
}

// phpMktime 构造时间戳；utc=true 为 gmmktime
func phpMktime(e *Env, a []Value, utc bool) Value {
	now := time.Now()
	hour, minute, second := now.Hour(), now.Minute(), now.Second()
	month, day, year := int(now.Month()), now.Day(), now.Year()
	get := func(i int, fallback int) int {
		if i < len(a) && !a[i].IsNull() {
			return int(a[i].ToInt())
		}
		return fallback
	}
	if len(a) > 0 {
		hour = get(0, hour)
	}
	if len(a) > 1 {
		minute = get(1, minute)
	}
	if len(a) > 2 {
		second = get(2, second)
	}
	if len(a) > 3 {
		month = get(3, month)
	}
	if len(a) > 4 {
		day = get(4, day)
	}
	if len(a) > 5 {
		year = get(5, year)
	}
	var loc *time.Location
	if utc {
		loc = time.UTC
	} else {
		loc = e.loc
		if loc == nil {
			loc = time.UTC
		}
	}
	return NewInt(time.Date(year, time.Month(month), day, hour, minute, second, 0, loc).Unix())
}
