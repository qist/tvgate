package phpgo

import (
	"os"
	"testing"
)

// TestParseLive4gtv 验证真实脚本 4gtv.php 能被纯 Go parser 完整解析。
func TestParseLive4gtv(t *testing.T) {
	src, err := os.ReadFile("/www/4gtv.php")
	if err != nil {
		t.Skip("4gtv.php not found, skip")
	}
	if _, err := ParseProgram(string(src)); err != nil {
		t.Fatalf("parse 4gtv.php failed: %v", err)
	}
}

// TestPregLookbehind 验证 lookbehind/lookahead 在新 regexp2 内核下原生可用
// （原 RE2 内核时代靠字符串级 workaround，现改为 PCRE 真语义）。
func TestPregLookbehind(t *testing.T) {
	b := "http://x/abc.m3u8"
	got, err := pregBuiltinReplace(nil, []Value{
		NewString(`(?<=\/)[^\/.]+(?=\.m3u8)`),
		NewString("replaced"),
		NewString(b),
	})
	if err != nil {
		t.Fatalf("preg_replace err: %v", err)
	}
	want := "http://x/replaced.m3u8"
	if got.ToString() != want {
		t.Fatalf("lookbehind workaround: got %q want %q", got.ToString(), want)
	}
}

// TestBuiltinsRegistered 确认关键内置函数已注册。
func TestBuiltinsRegistered(t *testing.T) {
	for _, name := range []string{
		"curl_init", "curl_setopt", "curl_exec", "curl_close",
		"openssl_encrypt", "openssl_decrypt", "base64_encode", "base64_decode",
		"json_encode", "json_decode", "preg_match", "preg_replace",
		"file_get_contents", "substr", "strpos", "urlencode", "array_key_exists",
	} {
		if _, ok := builtins[name]; !ok {
			t.Fatalf("builtin %s not registered", name)
		}
	}
}
