package phpgo

import (
	"os"
)

func init() {
	// fopen：打开文件，返回资源（用 fd 整数表示）
	builtins["fopen"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewBool(false), nil
		}
		path := a[0].ToString()
		mode := a[1].ToString()
		var flag int
		var perm os.FileMode = 0644
		switch mode {
		case "r":
			flag = os.O_RDONLY
		case "r+":
			flag = os.O_RDWR
		case "w":
			flag = os.O_CREATE | os.O_TRUNC | os.O_WRONLY
		case "w+":
			flag = os.O_CREATE | os.O_TRUNC | os.O_RDWR
		case "a":
			flag = os.O_CREATE | os.O_APPEND | os.O_WRONLY
		case "a+":
			flag = os.O_CREATE | os.O_APPEND | os.O_RDWR
		case "c":
			flag = os.O_CREATE | os.O_WRONLY
		case "c+":
			flag = os.O_CREATE | os.O_RDWR
		case "x":
			flag = os.O_CREATE | os.O_EXCL | os.O_WRONLY
		case "x+":
			flag = os.O_CREATE | os.O_EXCL | os.O_RDWR
		default:
			flag = os.O_RDONLY
		}
		// 与 file_get_contents/file_put_contents 一致：相对路径按脚本目录解析，
		// 否则 fopen 与它们基准不同会导致"写一处读另一处"
		f, err := os.OpenFile(e.ResolvePath(path), flag, perm)
		if err != nil {
			return NewBool(false), nil
		}
		fd := e.nextFd
		e.nextFd++
		e.files[fd] = f
		return NewInt(int64(fd)), nil
	}

	// fclose：关闭文件句柄
	builtins["fclose"] = func(e *Env, a []Value) (Value, error) {
		if len(a) == 0 {
			return NewBool(false), nil
		}
		fd := int(a[0].ToInt())
		f, ok := e.files[fd]
		if !ok {
			return NewBool(false), nil
		}
		f.Close()
		delete(e.files, fd)
		return NewBool(true), nil
	}

	// fread：从句柄读取指定长度字节。
	// PHP 语义是「最多读 length，读到多少返回多少」（网络流单次 recv），
	// 不能用 io.ReadFull 等满——否则 socket 上小帧数据会一直阻塞到超时。
	builtins["fread"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewString(""), nil
		}
		fd := int(a[0].ToInt())
		n := int(a[1].ToInt())
		f, ok := e.files[fd]
		if !ok || n <= 0 {
			return NewString(""), nil
		}
		buf := make([]byte, n)
		m, err := f.Read(buf)
		if err != nil && m == 0 {
			return NewString(""), nil
		}
		return NewString(string(buf[:m])), nil
	}

	// fwrite：向句柄写入内容，返回写入字节数
	builtins["fwrite"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 2 {
			return NewInt(0), nil
		}
		fd := int(a[0].ToInt())
		data := a[1].ToString()
		f, ok := e.files[fd]
		if !ok {
			return NewInt(0), nil
		}
		m, err := f.Write([]byte(data))
		if err != nil {
			return NewInt(int64(m)), nil
		}
		return NewInt(int64(m)), nil
	}
}
