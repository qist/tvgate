package phpgo

import (
	"os"
	"syscall"
	"time"
)

func init() {
	builtins["rename"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		err := os.Rename(e.ResolvePath(a[0].ToString()), e.ResolvePath(a[1].ToString()))
		return NewBool(err == nil), nil
	}
	builtins["copy"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		data, err := os.ReadFile(e.ResolvePath(a[0].ToString()))
		if err != nil {
			return NewBool(false), nil
		}
		err = os.WriteFile(e.ResolvePath(a[1].ToString()), data, 0644)
		return NewBool(err == nil), nil
	}
	builtins["touch"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		path := e.ResolvePath(a[0].ToString())
		now := time.Now()
		if _, err := os.Stat(path); err != nil {
			f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				return NewBool(false), nil
			}
			f.Close()
		}
		err := os.Chtimes(path, now, now)
		return NewBool(err == nil), nil
	}
	// readfile：输出文件内容，返回字节数
	builtins["readfile"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewInt(0), nil
		}
		data, err := os.ReadFile(e.ResolvePath(a[0].ToString()))
		if err != nil {
			return NewInt(0), nil
		}
		e.writeOutput(string(data))
		return NewInt(int64(len(data))), nil
	}
	// fseek：定位文件指针（SEEK_SET/SEEK_CUR/SEEK_END）
	builtins["fseek"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewInt(-1), nil
		}
		fd := int(a[0].ToInt())
		offset := a[1].ToInt()
		whence := 0 // SEEK_SET
		if len(a) >= 3 {
			whence = int(a[2].ToInt())
		}
		f, ok := e.files[fd].(*os.File)
		if !ok {
			return NewInt(-1), nil
		}
		n, err := f.Seek(offset, whence)
		if err != nil {
			return NewInt(-1), nil
		}
		return NewInt(n), nil
	}
	// ftell：返回文件指针位置
	builtins["ftell"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewInt(-1), nil
		}
		f, ok := e.files[int(a[0].ToInt())].(*os.File)
		if !ok {
			return NewInt(-1), nil
		}
		n, err := f.Seek(0, 1)
		if err != nil {
			return NewInt(-1), nil
		}
		return NewInt(n), nil
	}
	// rewind：指针回到文件开头
	builtins["rewind"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		f, ok := e.files[int(a[0].ToInt())].(*os.File)
		if !ok {
			return NewBool(false), nil
		}
		_, err := f.Seek(0, 0)
		return NewBool(err == nil), nil
	}
	builtins["fileatime"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		fi, err := os.Stat(e.ResolvePath(a[0].ToString()))
		if err != nil {
			return NewBool(false), nil
		}
		return NewInt(fi.ModTime().Unix()), nil
	}
	// ftruncate：截断文件到指定长度
	builtins["ftruncate"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		f, ok := e.files[int(a[0].ToInt())].(*os.File)
		if !ok {
			return NewBool(false), nil
		}
		return NewBool(f.Truncate(a[1].ToInt()) == nil), nil
	}
	// fflush：把缓冲内容刷入磁盘（Go 直接写文件，Sync 保证落盘）
	builtins["fflush"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		f, ok := e.files[int(a[0].ToInt())].(*os.File)
		if !ok {
			return NewBool(false), nil
		}
		return NewBool(f.Sync() == nil), nil
	}
	// flock：文件锁（LOCK_SH=1/LOCK_EX=2/LOCK_UN=3/LOCK_NB=4）
	builtins["flock"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		f, ok := e.files[int(a[0].ToInt())].(*os.File)
		if !ok {
			return NewBool(false), nil
		}
		op := int(a[1].ToInt())
		var how int
		// PHP 常量：LOCK_SH=1 LOCK_EX=2 LOCK_UN=3 LOCK_NB=4
		switch op &^ 4 {
		case 1:
			how = syscall.LOCK_SH
		case 2:
			how = syscall.LOCK_EX
		case 3:
			how = syscall.LOCK_UN
		default:
			return NewBool(false), nil
		}
		if op&4 != 0 {
			how |= syscall.LOCK_NB
		}
		return NewBool(syscall.Flock(int(f.Fd()), how) == nil), nil
	}
	builtins["filectime"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		fi, err := os.Stat(e.ResolvePath(a[0].ToString()))
		if err != nil {
			return NewBool(false), nil
		}
		return NewInt(fi.ModTime().Unix()), nil
	}
}
