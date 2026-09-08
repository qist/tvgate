package phpgo

// 文件系统扩展补齐：stat/lstat/filetype/is_link/clearstatcache/chmod/symlink/readlink/
// getcwd/chdir/fnmatch/tempnam/tmpfile/is_readable/is_executable/fscanf/fpassthru/
// move_uploaded_file/is_uploaded_file。

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// phpBuildStat 构造 PHP stat()/lstat() 返回数组（数字键 + 关联键）。
func phpBuildStat(fi os.FileInfo) Value {
	mode := int64(fi.Mode().Perm())
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		mode |= 0o120000
	case fi.IsDir():
		mode |= 0o040000
	case fi.Mode().IsRegular():
		mode |= 0o100000
	case fi.Mode()&os.ModeNamedPipe != 0:
		mode |= 0o010000
	case fi.Mode()&os.ModeSocket != 0:
		mode |= 0o140000
	case fi.Mode()&os.ModeDevice != 0:
		if fi.Mode()&os.ModeCharDevice != 0 {
			mode |= 0o020000
		} else {
			mode |= 0o060000
		}
	}
	mt := fi.ModTime()
	names := map[string]int64{
		"dev": 0, "ino": 0, "nlink": 1, "uid": 0, "gid": 0, "rdev": 0,
		"size": fi.Size(), "atime": mt.Unix(), "mtime": mt.Unix(),
		"ctime": mt.Unix(), "blksize": 4096, "blocks": (fi.Size()+511)/512,
	}
	names["mode"] = mode
	names["nlink"] = 1
	order := []string{"dev", "ino", "mode", "nlink", "uid", "gid", "rdev", "size", "atime", "mtime", "ctime", "blksize", "blocks"}
	arr := NewArray()
	for i, k := range order {
		arr.ArraySet(NewString(k), NewInt(names[k]))
		arr.ArraySet(NewInt(int64(i)), NewInt(names[k]))
	}
	return arr
}

// phpFileTypeOf 返回 PHP filetype() 的字符串
func phpFileTypeOf(fi os.FileInfo) string {
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		return "link"
	case fi.IsDir():
		return "dir"
	case fi.Mode().IsRegular():
		return "file"
	case fi.Mode()&os.ModeNamedPipe != 0:
		return "fifo"
	case fi.Mode()&os.ModeSocket != 0:
		return "socket"
	case fi.Mode()&os.ModeDevice != 0:
		if fi.Mode()&os.ModeCharDevice != 0 {
			return "char"
		}
		return "block"
	}
	return "unknown"
}

// fnmatchToRegex 把 fnmatch 通配符转成正则表达式
func fnmatchToRegex(pattern string, caseFold bool) string {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		case '[':
			j := i + 1
			neg := false
			if j < len(pattern) && (pattern[j] == '!' || pattern[j] == '^') {
				neg = true
				j++
			}
			var cls strings.Builder
			if neg {
				cls.WriteString("^")
			}
			closed := false
			for ; j < len(pattern); j++ {
				if pattern[j] == ']' {
					closed = true
					break
				}
				if pattern[j] == '\\' && j+1 < len(pattern) {
					j++
					cls.WriteString(regexp.QuoteMeta(string(pattern[j])))
					continue
				}
				cls.WriteString(regexp.QuoteMeta(string(pattern[j])))
			}
			if !closed {
				b.WriteString("\\[")
				continue
			}
			b.WriteString("[" + cls.String() + "]")
			i = j
		case '\\':
			if i+1 < len(pattern) {
				i++
				b.WriteString(regexp.QuoteMeta(string(pattern[i])))
			} else {
				b.WriteString("\\\\")
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	re := b.String()
	if caseFold {
		re = "(?i)" + re
	}
	return re
}

func init() {
	builtins["stat"] = func(e *Env, a []Value) (Value, error) {
		return phpStatImpl(e, a, false)
	}
	builtins["lstat"] = func(e *Env, a []Value) (Value, error) {
		return phpStatImpl(e, a, true)
	}
	builtins["filetype"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		path := e.ResolvePath(a[0].ToString())
		fi, err := os.Lstat(path)
		if err != nil {
			return NewBool(false), nil
		}
		return NewString(phpFileTypeOf(fi)), nil
	}
	builtins["is_link"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		fi, err := os.Lstat(e.ResolvePath(a[0].ToString()))
		if err != nil {
			return NewBool(false), nil
		}
		return NewBool(fi.Mode()&os.ModeSymlink != 0), nil
	}
	builtins["clearstatcache"] = func(e *Env, a []Value) (Value, error) {
		return NewNull(), nil // 无状态缓存，no-op
	}
	builtins["chmod"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		err := os.Chmod(e.ResolvePath(a[0].ToString()), os.FileMode(a[1].ToInt()))
		return NewBool(err == nil), nil
	}
	builtins["chown"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		// PHP chown 第二参可为用户名或 uid；此处仅支持数值 uid（gid 保持不变传 -1）
		err := os.Chown(e.ResolvePath(a[0].ToString()), int(a[1].ToInt()), -1)
		return NewBool(err == nil), nil
	}
	builtins["symlink"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		target := e.ResolvePath(a[0].ToString())
		link := e.ResolvePath(a[1].ToString())
		err := os.Symlink(target, link)
		return NewBool(err == nil), nil
	}
	builtins["readlink"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		dest, err := os.Readlink(e.ResolvePath(a[0].ToString()))
		if err != nil {
			return NewBool(false), nil
		}
		return NewString(dest), nil
	}
	builtins["getcwd"] = func(e *Env, a []Value) (Value, error) {
		wd, err := os.Getwd()
		if err != nil {
			return NewBool(false), nil
		}
		return NewString(wd), nil
	}
	builtins["chdir"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		err := os.Chdir(e.ResolvePath(a[0].ToString()))
		return NewBool(err == nil), nil
	}
	builtins["is_readable"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		return NewBool(accessOK(e.ResolvePath(a[0].ToString()), 4)), nil
	}
	builtins["is_executable"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		return NewBool(accessOK(e.ResolvePath(a[0].ToString()), 1)), nil
	}
	builtins["fnmatch"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		flags := int64(0)
		if len(a) >= 3 {
			flags = a[2].ToInt()
		}
		re, err := regexp.Compile(fnmatchToRegex(a[0].ToString(), flags&16 != 0))
		if err != nil {
			return NewBool(false), nil
		}
		return NewBool(re.MatchString(a[1].ToString())), nil
	}
	builtins["tempnam"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		dir := a[0].ToString()
		if dir == "" {
			dir = os.TempDir()
		}
		f, err := os.CreateTemp(dir, a[1].ToString())
		if err != nil {
			return NewBool(false), nil
		}
		name := f.Name()
		f.Close()
		return NewString(name), nil
	}
	builtins["tmpfile"] = func(e *Env, a []Value) (Value, error) {
		f, err := os.CreateTemp("", "")
		if err != nil {
			return NewBool(false), nil
		}
		fd := e.nextFd
		e.nextFd++
		e.files[fd] = f
		return NewInt(int64(fd)), nil
	}
	// fscanf($handle, $format[, ...])：无额外参数时返回解析结果数组
	builtins["fscanf"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		fd := int(a[0].ToInt())
		f, ok := e.files[fd]
		if !ok {
			return NewBool(false), nil
		}
		phpFmt := a[1].ToString()
		goFmt, types, ok := phpScanFormat(phpFmt)
		if !ok {
			return NewBool(false), nil
		}
		ptrs := make([]interface{}, 0, len(types))
		vars := make([]interface{}, len(types))
		for i, t := range types {
			switch t {
			case "d":
				var v int
				vars[i] = &v
			case "f":
				var v float64
				vars[i] = &v
			case "s", "c":
				var v string
				vars[i] = &v
			}
			ptrs = append(ptrs, vars[i])
		}
		n, err := fmt.Fscanf(f, goFmt, ptrs...)
		if err != nil && n == 0 {
			return NewBool(false), nil
		}
		out := NewArray()
		for i := 0; i < n && i < len(vars); i++ {
			switch types[i] {
			case "d":
				out.ArraySet(NewInt(int64(len(out.Keys))), NewInt(int64(*(vars[i].(*int)))))
			case "f":
				out.ArraySet(NewInt(int64(len(out.Keys))), NewFloat(*(vars[i].(*float64))))
			case "s", "c":
				out.ArraySet(NewInt(int64(len(out.Keys))), NewString(*(vars[i].(*string))))
			}
		}
		return out, nil
	}
	// fpassthru($handle)：从当前位置读到 EOF 并输出，返回字节数
	builtins["fpassthru"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		fd := int(a[0].ToInt())
		f, ok := e.files[fd]
		if !ok {
			return NewBool(false), nil
		}
		data, err := io.ReadAll(f)
		if err != nil {
			return NewBool(false), nil
		}
		e.writeOutput(string(data))
		return NewInt(int64(len(data))), nil
	}
	builtins["is_uploaded_file"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		p := a[0].ToString()
		for _, v := range e.ufiles {
			tv := v.ArrayGet(NewString("tmp_name"))
			if tv.Kind == KindString && tv.Str == p {
				return NewBool(true), nil
			}
		}
		return NewBool(false), nil
	}
	builtins["move_uploaded_file"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		from := a[0].ToString()
		to := e.ResolvePath(a[1].ToString())
		matched := false
		for _, v := range e.ufiles {
			tv := v.ArrayGet(NewString("tmp_name"))
			if tv.Kind == KindString && tv.Str == from {
				matched = true
				break
			}
		}
		if !matched {
			return NewBool(false), nil
		}
		// 先尝试 rename，跨设备失败则复制+删除
		if err := os.Rename(from, to); err == nil {
			return NewBool(true), nil
		}
		data, err := os.ReadFile(from)
		if err != nil {
			return NewBool(false), nil
		}
		if err := os.WriteFile(to, data, 0o644); err != nil {
			return NewBool(false), nil
		}
		os.Remove(from)
		return NewBool(true), nil
	}
}

// phpStatImpl stat/lstat 公共实现
func phpStatImpl(e *Env, a []Value, lstat bool) (Value, error) {
	if len(a) < 1 {
		return NewBool(false), nil
	}
	path := e.ResolvePath(a[0].ToString())
	var fi os.FileInfo
	var err error
	if lstat {
		fi, err = os.Lstat(path)
	} else {
		fi, err = os.Stat(path)
	}
	if err != nil {
		return NewBool(false), nil
	}
	return phpBuildStat(fi), nil
}

// accessOK 权限探测（读 r=4 / 写 w=2 / 执行 x=1）
func accessOK(path string, want int) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	if want == 0 {
		return true
	}
	perm := fi.Mode().Perm()
	if want&4 != 0 && perm&0o444 == 0 {
		return false
	}
	if want&2 != 0 && perm&0o222 == 0 {
		return false
	}
	if want&1 != 0 && perm&0o111 == 0 {
		return false
	}
	return true
}

// phpScanFormat 把 PHP fscanf 格式转换为 Go fmt 扫描格式。
// types 记录每个转换的类型（d/f/s/c）。
func phpScanFormat(php string) (string, []string, bool) {
	var b strings.Builder
	var types []string
	i := 0
	for i < len(php) {
		c := php[i]
		if c != '%' {
			b.WriteByte(c)
			i++
			continue
		}
		if i+1 >= len(php) {
			return "", nil, false
		}
		// 处理转换
		j := i + 1
		if php[j] == '%' {
			b.WriteString("%%")
			i += 2
			continue
		}
		// 跳过 PHP 独有的标志/宽度（- + 空格 0 与 * 等）
		for j < len(php) && strings.IndexByte("-+ 0'0123456789*", php[j]) >= 0 {
			j++
		}
		if j >= len(php) {
			return "", nil, false
		}
		spec := php[j]
		switch spec {
		case 'd':
			b.WriteString("%d")
			types = append(types, "d")
		case 'u':
			b.WriteString("%d")
			types = append(types, "d")
		case 'f', 'e', 'g', 'E', 'G':
			b.WriteString("%f")
			types = append(types, "f")
		case 's':
			b.WriteString("%s")
			types = append(types, "s")
		case 'c':
			b.WriteString("%c")
			types = append(types, "c")
		case 'x':
			b.WriteString("%x")
			types = append(types, "d")
		default:
			return "", nil, false
		}
		i = j + 1
	}
	return b.String(), types, true
}

// 编译期避免未使用告警
var _ = filepath.Join
