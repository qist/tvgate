//go:build unix

package phpgo

// disk_free_space / disk_total_space（Unix：通过 statfs 取真实数值）。

import "syscall"

func init() {
	builtins["disk_free_space"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		var st syscall.Statfs_t
		if err := syscall.Statfs(e.ResolvePath(a[0].ToString()), &st); err != nil {
			return NewBool(false), nil
		}
		return NewFloat(float64(st.Bavail) * float64(st.Bsize)), nil
	}
	builtins["disk_total_space"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		var st syscall.Statfs_t
		if err := syscall.Statfs(e.ResolvePath(a[0].ToString()), &st); err != nil {
			return NewBool(false), nil
		}
		return NewFloat(float64(st.Blocks) * float64(st.Bsize)), nil
	}
}
