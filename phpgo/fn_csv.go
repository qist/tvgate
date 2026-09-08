package phpgo

// CSV 相关：str_getcsv / fgetcsv / fputcsv。
// 逐字符状态机解析，支持引号内换行与转义（与 PHP 的字段语义一致）。

import (
	"io"
	"strings"
)

// phpReadCSVRecord 从 r 读取一个 CSV 记录。
// 返回字段列表；eof=true 表示在记录开始前已到文件末尾。
// 一次只消费一个记录（含结尾换行），适合文件句柄连续多次调用。
func phpReadCSVRecord(r io.Reader, delim, encl, esc byte) (fields []string, eof bool) {
	readByte := func() (byte, bool) {
		buf := make([]byte, 1)
		n, err := r.Read(buf)
		if err != nil || n == 0 {
			return 0, false
		}
		return buf[0], true
	}
	fields = []string{}
	var b strings.Builder
	inQuotes := false
	started := false // 记录中是否已出现过任何字符（区分空行与 EOF）
	for {
		c, ok := readByte()
		if !ok {
			if !started {
				return nil, true
			}
			fields = append(fields, b.String())
			return fields, false
		}
		started = true
		if inQuotes {
			if c == encl {
				// 可能为转义的双引号（""）
				nxt, ok2 := readByte()
				if ok2 && nxt == encl {
					b.WriteByte(encl)
					continue
				}
				inQuotes = false
				// 回放 nxt（可能是分隔符/换行/EOF）
				if !ok2 {
					fields = append(fields, b.String())
					return fields, false
				}
				if nxt == delim {
					fields = append(fields, b.String())
					b.Reset()
					continue
				}
				if nxt == '\n' {
					fields = append(fields, b.String())
					return fields, false
				}
				if nxt == '\r' {
					continue
				}
				// 引号后出现非分隔符：按字面并入（宽松处理）
				b.WriteByte(nxt)
				continue
			}
			if c == esc {
				nxt, ok2 := readByte()
				if ok2 && (nxt == esc || nxt == encl) {
					b.WriteByte(nxt)
					continue
				}
				// 孤立转义符：保留自身
				b.WriteByte(esc)
				if ok2 {
					b.WriteByte(nxt)
				}
				continue
			}
			b.WriteByte(c)
			continue
		}
		// 不在引号内
		switch c {
		case '\r':
			continue
		case '\n':
			fields = append(fields, b.String())
			return fields, false
		case delim:
			fields = append(fields, b.String())
			b.Reset()
		case encl:
			if b.Len() == 0 {
				inQuotes = true
				continue
			}
			// 引号不在字段开头：PHP 中通常视为字面字符，此处宽松追加
			b.WriteByte(c)
		case esc:
			nxt, ok2 := readByte()
			if ok2 && (nxt == delim || nxt == encl || nxt == esc) {
				// 引号外 PHP 不会转义；仅在 enclosure==escape 的场合下 PHP 用双引号表达
				b.WriteByte(c)
				b.WriteByte(nxt)
				continue
			}
			b.WriteByte(c)
			if ok2 {
				b.WriteByte(nxt)
			}
		default:
			b.WriteByte(c)
		}
	}
}

// phpWriteCSVRecord 把字段数组格式化为一行 CSV（含行尾换行）写入 w。
// 返回写入字节数。按 PHP 规则：字段含分隔符/引号/换行/回车/制表符或首尾空格时加引号。
func phpWriteCSVRecord(w io.Writer, fields []string, delim, encl, esc byte) (int, error) {
	var b strings.Builder
	for i, f := range fields {
		if i > 0 {
			b.WriteByte(delim)
		}
		needQuote := false
		if strings.ContainsAny(f, string([]byte{delim, encl, esc, '\n', '\r', '\t'})) {
			needQuote = true
		} else if len(f) > 0 && (f[0] == ' ' || f[len(f)-1] == ' ') {
			needQuote = true
		}
		if !needQuote {
			b.WriteString(f)
			continue
		}
		b.WriteByte(encl)
		var sb strings.Builder
		for j := 0; j < len(f); j++ {
			ch := f[j]
			if ch == encl {
				sb.WriteByte(encl)
			}
			sb.WriteByte(ch)
		}
		b.WriteString(sb.String())
		b.WriteByte(encl)
	}
	b.WriteByte('\n')
	return w.Write([]byte(b.String()))
}

func init() {
	builtins["str_getcsv"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewArray(), nil
		}
		src := a[0].ToString()
		delim, encl, esc := byte(','), byte('"'), byte('\\')
		if len(a) >= 2 {
			delim = firstByte(a[1].ToString(), delim)
		}
		if len(a) >= 3 {
			encl = firstByte(a[2].ToString(), encl)
		}
		if len(a) >= 4 {
			esc = firstByte(a[3].ToString(), esc)
		}
		fields, _ := phpReadCSVRecord(strings.NewReader(src), delim, encl, esc)
		out := NewArray()
		for _, f := range fields {
			out.ArraySet(NewInt(int64(len(out.Keys))), NewString(f))
		}
		return out, nil
	}
	// fgetcsv($handle[, $length[, $delimiter[, $enclosure[, $escape]]]])：读取并解析一行
	builtins["fgetcsv"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		fd := int(a[0].ToInt())
		f, ok := e.files[fd]
		if !ok {
			return NewBool(false), nil
		}
		delim, encl, esc := byte(','), byte('"'), byte('\\')
		if len(a) >= 3 {
			delim = firstByte(a[2].ToString(), delim)
		}
		if len(a) >= 4 {
			encl = firstByte(a[3].ToString(), encl)
		}
		if len(a) >= 5 {
			esc = firstByte(a[4].ToString(), esc)
		}
		fields, eof := phpReadCSVRecord(f, delim, encl, esc)
		if eof {
			return NewBool(false), nil
		}
		out := NewArray()
		for _, fld := range fields {
			out.ArraySet(NewInt(int64(len(out.Keys))), NewString(fld))
		}
		return out, nil
	}
	// fputcsv($handle, $fields[, $delimiter[, $enclosure]]])：写一行，返回字节数
	builtins["fputcsv"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewInt(0), nil
		}
		fd := int(a[0].ToInt())
		f, ok := e.files[fd]
		if !ok {
			return NewBool(false), nil
		}
		arr := deref(a[1])
		if arr.Kind != KindArray {
			return NewBool(false), nil
		}
		delim, encl := byte(','), byte('"')
		if len(a) >= 3 {
			delim = firstByte(a[2].ToString(), delim)
		}
		if len(a) >= 4 {
			encl = firstByte(a[3].ToString(), encl)
		}
		var fields []string
		for _, k := range arr.Keys {
			fields = append(fields, arr.Arr[k].ToString())
		}
		n, err := phpWriteCSVRecord(f, fields, delim, encl, '\\')
		if err != nil {
			return NewBool(false), nil
		}
		return NewInt(int64(n)), nil
	}
}

// firstByte 取字符串首字节；空串返回默认
func firstByte(s string, def byte) byte {
	if s == "" {
		return def
	}
	return s[0]
}
