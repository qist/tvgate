//go:build !unix

package phpgo

// disk_free_space / disk_total_space（非 Unix 平台：无 statfs，返回 false）。

func init() {
	builtins["disk_free_space"] = func(e *Env, a []Value) (Value, error) {
		return NewBool(false), nil
	}
	builtins["disk_total_space"] = func(e *Env, a []Value) (Value, error) {
		return NewBool(false), nil
	}
}
