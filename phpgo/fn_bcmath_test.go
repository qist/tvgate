package phpgo

import (
	"testing"
)

func callOne(t *testing.T, fn string, args ...Value) Value {
	t.Helper()
	target, ok := builtins[fn]
	if !ok {
		t.Fatalf("builtin %s not registered", fn)
	}
	v, err := target(nil, args)
	if err != nil {
		t.Fatalf("%s err: %v", fn, err)
	}
	return v
}

func TestSysGetTempDir(t *testing.T) {
	v := callOne(t, "sys_get_temp_dir")
	if v.ToString() == "" {
		t.Fatal("sys_get_temp_dir returned empty")
	}
}

// TestBcmathBasics 验证 wxty.php RSA 解密依赖的 bc 原语。
func TestBcmathBasics(t *testing.T) {
	cases := []struct {
		fn   string
		args []string
		want string
	}{
		{"bcadd", []string{"123", "456"}, "579"},
		{"bcmul", []string{"16", "16"}, "256"},
		{"bcmod", []string{"257", "16"}, "1"},
		{"bcdiv", []string{"100", "16", "0"}, "6"},
		{"bcdiv", []string{"100", "16"}, "6"},
		{"bcpowmod", []string{"3", "4", "5"}, "1"}, // 3^4 mod 5 = 81 mod 5 = 1
		{"bccomp", []string{"5", "5"}, "0"},
		{"bccomp", []string{"7", "5"}, "1"},
		{"bccomp", []string{"5", "7"}, "-1"},
	}
	for _, c := range cases {
		args := make([]Value, len(c.args))
		for i, s := range c.args {
			args[i] = NewString(s)
		}
		got := callOne(t, c.fn, args...).ToString()
		if got != c.want {
			t.Errorf("%s(%v) = %q, want %q", c.fn, c.args, got, c.want)
		}
	}
}

// TestBcmathRsaHex 复现 rsaHexToDec/rsaDecToHex 的大整数往返。
func TestBcmathRsaHex(t *testing.T) {
	// 0x10 * 0x10 + 0x10 = 0x110 = 272
	v := callOne(t, "bcadd",
		NewString(callOne(t, "bcmul",
			NewString("16"), NewString("16")).ToString()), // 256
		NewString("16")).ToString() // 272
	if v != "272" {
		t.Fatalf("0x110 decode = %q, want 272", v)
	}
}