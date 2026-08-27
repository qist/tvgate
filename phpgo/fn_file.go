package phpgo

import (
	"os"
	"path/filepath"
	"strings"
)

func init() {
	builtins["file_put_contents"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		path := e.ResolvePath(a[0].ToString())
		data := a[1].ToString()
		// 确保目录存在
		dir := filepath.Dir(path)
		os.MkdirAll(dir, 0755)
		err := os.WriteFile(path, []byte(data), 0644)
		if err != nil {
			return NewBool(false), nil
		}
		return NewInt(int64(len(data))), nil
	}
	builtins["file_exists"] = func(e *Env, a []Value) (Value, error) {
		_, err := os.Stat(e.ResolvePath(a[0].ToString()))
		return NewBool(err == nil), nil
	}
	builtins["is_dir"] = func(e *Env, a []Value) (Value, error) {
		fi, err := os.Stat(e.ResolvePath(a[0].ToString()))
		if err != nil {
			return NewBool(false), nil
		}
		return NewBool(fi.IsDir()), nil
	}
	builtins["is_file"] = func(e *Env, a []Value) (Value, error) {
		fi, err := os.Stat(e.ResolvePath(a[0].ToString()))
		if err != nil {
			return NewBool(false), nil
		}
		return NewBool(!fi.IsDir()), nil
	}
	builtins["is_writable"] = func(e *Env, a []Value) (Value, error) {
		fi, err := os.Stat(e.ResolvePath(a[0].ToString()))
		if err != nil {
			return NewBool(false), nil
		}
		_ = fi
		return NewBool(true), nil
	}
	builtins["unlink"] = func(e *Env, a []Value) (Value, error) {
		err := os.Remove(e.ResolvePath(a[0].ToString()))
		if err != nil {
			return NewBool(false), nil
		}
		return NewBool(true), nil
	}
	builtins["mkdir"] = func(e *Env, a []Value) (Value, error) {
		mode := os.FileMode(0755)
		if len(a) >= 2 {
			mode = os.FileMode(a[1].ToInt())
		}
		recursive := false
		if len(a) >= 3 {
			recursive = a[2].ToBool()
		}
		path := e.ResolvePath(a[0].ToString())
		var err error
		if recursive {
			err = os.MkdirAll(path, mode)
		} else {
			err = os.Mkdir(path, mode)
		}
		if err != nil {
			return NewBool(false), nil
		}
		return NewBool(true), nil
	}
	builtins["file"] = func(e *Env, a []Value) (Value, error) {
		data, err := os.ReadFile(e.ResolvePath(a[0].ToString()))
		if err != nil {
			return NewBool(false), nil
		}
		// PHP flags: FILE_IGNORE_NEW_LINES=2, FILE_SKIP_EMPTY_LINES=4
		skipEmptyLines := false
		if len(a) >= 2 {
			flags := int(a[1].ToInt())
			// FILE_IGNORE_NEW_LINES=2: 行末无换行符（phpgo 已默认去除 \r，此标志无需额外处理）
			// FILE_SKIP_EMPTY_LINES=4: 跳过空行
			if flags&4 != 0 {
				skipEmptyLines = true
			}
		}
		lines := strings.Split(string(data), "\n")
		arr := NewArray()
		for _, l := range lines {
			l = strings.TrimRight(l, "\r")
			if skipEmptyLines && l == "" {
				continue
			}
			arr.ArraySet(NewInt(int64(len(arr.Keys))), NewString(l))
		}
		return arr, nil
	}
	builtins["dirname"] = func(e *Env, a []Value) (Value, error) {
		path := a[0].ToString()
		idx := strings.LastIndexByte(path, '/')
		if idx < 0 {
			return NewString("."), nil
		}
		if idx == 0 {
			return NewString("/"), nil
		}
		return NewString(path[:idx]), nil
	}
	builtins["basename"] = func(e *Env, a []Value) (Value, error) {
		path := a[0].ToString()
		suffix := ""
		if len(a) >= 2 {
			suffix = a[1].ToString()
		}
		idx := strings.LastIndexByte(path, '/')
		if idx >= 0 {
			path = path[idx+1:]
		}
		if suffix != "" && strings.HasSuffix(path, suffix) {
			path = strings.TrimSuffix(path, suffix)
		}
		return NewString(path), nil
	}
	builtins["realpath"] = func(e *Env, a []Value) (Value, error) {
		p, err := filepath.Abs(e.ResolvePath(a[0].ToString()))
		if err != nil {
			return NewBool(false), nil
		}
		_, err = os.Stat(p)
		if err != nil {
			return NewBool(false), nil
		}
		return NewString(p), nil
	}
	builtins["pathinfo"] = func(e *Env, a []Value) (Value, error) {
		path := a[0].ToString()
		result := NewArray()
		idx := strings.LastIndexByte(path, '/')
		dirName := ""
		baseName := path
		if idx >= 0 {
			dirName = path[:idx]
			baseName = path[idx+1:]
		}
		extIdx := strings.LastIndexByte(baseName, '.')
		ext := ""
		filename := baseName
		if extIdx > 0 {
			ext = baseName[extIdx+1:]
			filename = baseName[:extIdx]
		}
		result.ArraySet(NewString("dirname"), NewString(dirName))
		result.ArraySet(NewString("basename"), NewString(baseName))
		if ext != "" {
			result.ArraySet(NewString("extension"), NewString(ext))
		}
		result.ArraySet(NewString("filename"), NewString(filename))
		return result, nil
	}
	builtins["filesize"] = func(e *Env, a []Value) (Value, error) {
		fi, err := os.Stat(e.ResolvePath(a[0].ToString()))
		if err != nil {
			return NewBool(false), nil
		}
		return NewInt(fi.Size()), nil
	}
	builtins["filemtime"] = func(e *Env, a []Value) (Value, error) {
		fi, err := os.Stat(e.ResolvePath(a[0].ToString()))
		if err != nil {
			return NewBool(false), nil
		}
		return NewInt(fi.ModTime().Unix()), nil
	}
	// scandir：返回目录条目数组（数字索引）
	builtins["scandir"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewArray(), nil
		}
		entries, err := os.ReadDir(e.ResolvePath(a[0].ToString()))
		if err != nil {
			return NewBool(false), nil
		}
		arr := NewArray()
		for _, ent := range entries {
			arr.ArraySet(NewInt(int64(len(arr.Keys))), NewString(ent.Name()))
		}
		return arr, nil
	}
	// glob：按通配符匹配文件路径
	builtins["glob"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewArray(), nil
		}
		matches, err := filepath.Glob(e.ResolvePath(a[0].ToString()))
		if err != nil {
			return NewArray(), nil
		}
		arr := NewArray()
		for _, m := range matches {
			arr.ArraySet(NewInt(int64(len(arr.Keys))), NewString(m))
		}
		return arr, nil
	}
	// rmdir：删除空目录
	builtins["rmdir"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewBool(false), nil
		}
		err := os.Remove(e.ResolvePath(a[0].ToString()))
		if err != nil {
			return NewBool(false), nil
		}
		return NewBool(true), nil
	}
}
