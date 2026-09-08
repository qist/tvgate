package phpgo

import (
	"strings"
	"testing"
)

// TestMD5CryptVectors 校验 MD5-crypt 与 glibc（python3 crypt）输出一致
func TestMD5CryptVectors(t *testing.T) {
	cases := []struct {
		pw, salt, want string
	}{
		{"password", "saltstri", "$1$saltstri$qQY4WxjABChYG1ccLpfkz/"},
		{"hello", "abcdefgh", "$1$abcdefgh$rwnEbRiN0agqVgZBovWNQ/"},
		{"p@ss w0rd", "short", "$1$short$xBW6FzqsBkdEl7eMpZFFj."},
		{"中文密码", "abcd", "$1$abcd$tEwZHdQJ3pPnmeg0cMNPD/"},
	}
	for _, c := range cases {
		if got := phpMD5Crypt(c.pw, c.salt); got != c.want {
			t.Errorf("md5crypt(%q,%q) = %q, want %q", c.pw, c.salt, got, c.want)
		}
	}
}

func TestFuncArgsFamily(t *testing.T) {
	out := runPHP(t, `<?php
function f($a, $b = 9, $c = "x") {
	return json_encode(func_get_args()) . "|" . func_num_args() . "|" . func_get_arg(1);
}
echo f(1, 2);
function rec($n) { if ($n <= 0) { return func_num_args() . ":" . func_get_arg(0); } return rec($n - 1); }
echo "|" . rec(3);
`)
	want := "[1,2,\"x\"]|3|2|1:0"
	if out != want {
		t.Errorf("func_get_args family mismatch: got %q want %q", out, want)
	}
}

func TestVersionCompare(t *testing.T) {
	cases := []string{
		`version_compare("1.0.0","1.0.1")`,
		`version_compare("1.2.9","1.2.10")`,
		`version_compare("1.0.0-alpha","1.0.0")`,
		`version_compare("1.0.0rc1","1.0.0")`,
		`version_compare("1.0.0pl1","1.0.0")`,
		`version_compare("1.0.0","1.0")`,
		`version_compare("8.1.24","7.4.0",">=")?1:0`,
		`version_compare("1.0","1.0.0",">=")?1:0`,
	}
	want := []string{"-1", "-1", "-1", "-1", "1", "0", "1", "1"}
	for i, expr := range cases {
		if got := runPHP(t, "<?php echo "+expr+";"); got != want[i] {
			t.Errorf("%s = %q, want %q", expr, got, want[i])
		}
	}
}

func TestPasswordHashVerify(t *testing.T) {
	out := runPHP(t, `<?php
$h = password_hash("secret", PASSWORD_DEFAULT);
echo substr($h, 0, 4) . "|" . (password_verify("secret", $h) ? "ok" : "bad") . "|" .
     (password_verify("nope", $h) ? "bad" : "ok") . "|" .
     (password_needs_rehash($h, PASSWORD_DEFAULT, ["cost" => 10]) ? "y" : "n");
`)
	want := "$2y$|ok|ok|n"
	if out != want {
		t.Errorf("password_* mismatch: got %q want %q", out, want)
	}
}

func TestArrayExtras(t *testing.T) {
	out := runPHP(t, `<?php
$a = ["a" => 1, "b" => 2];
echo json_encode(array_replace($a, ["b" => 9, "c" => 3]));
echo "|" . json_encode(array_replace_recursive(["x" => ["p" => 1]], ["x" => ["q" => 2]]));
echo "|" . json_encode(array_change_key_case(["Abc" => 1, "dEF" => 2], CASE_UPPER));
echo "|" . json_encode(array_intersect_assoc(["a" => 1, "b" => 2], ["a" => 1, "b" => 3]));
echo "|" . json_encode(array_diff_assoc(["a" => 1, "b" => 2], ["a" => 1, "b" => 3]));
echo "|" . (array_is_list([0 => "x", 1 => "y"]) ? "L" : "N");
echo "|" . (array_is_list(["x" => 1]) ? "L" : "N");
$s = [3, 1, 2];
array_multisort($s);
echo "|" . json_encode($s);
`)
	want := `{"a":1,"b":9,"c":3}|{"x":{"p":1,"q":2}}|{"ABC":1,"DEF":2}|{"a":1}|{"b":2}|L|N|[1,2,3]`
	if out != want {
		t.Errorf("array extras mismatch: got %q want %q", out, want)
	}
}

func TestMiscNewFuncs(t *testing.T) {
	out := runPHP(t, `<?php
$lines = [];
$lines[] = strspn("abc123", "abc");
$lines[] = strcspn("abc123", "123");
$lines[] = substr_compare("hello world", "world", 6);
$lines[] = levenshtein("kitten", "sitting");
$lines[] = json_validate('{"a":1}') ? 1 : 0;
$lines[] = mb_convert_encoding("你好", "GBK") !== false ? 1 : 0;
$ini = parse_ini_string("k=v\n[sec]\na=1", true, INI_SCANNER_TYPED);
$lines[] = $ini["sec"]["a"];
$lines[] = function_exists("array_multisort") ? 1 : 0;
echo implode("|", $lines);
echo "|" . phpversion();
`)
	// 关键字段逐个校验
	parts := strings.Split(out, "|")
	want := []string{"3", "3", "0", "3", "1", "1", "1", "1"}
	if len(parts) < 9 {
		t.Fatalf("misc funcs output too short: %q", out)
	}
	for i, w := range want {
		if parts[i] != w {
			t.Errorf("misc[%d] = %q, want %q (full %q)", i, parts[i], w, out)
		}
	}
	if !strings.HasPrefix(parts[8], "8.1.24") {
		t.Errorf("phpversion prefix wrong: %q", parts[8])
	}
}

func TestShutdownAndErrorHandler(t *testing.T) {
	out := runPHP(t, `<?php
register_shutdown_function(function(){ echo "SHUTDOWN;"; });
set_error_handler(function($no, $msg){ echo "HANDLED:" . $msg . ";"; });
trigger_error("boom", E_USER_WARNING);
restore_error_handler();
echo "END;";
`)
	want := "HANDLED:boom;END;SHUTDOWN;"
	if out != want {
		t.Errorf("shutdown/errhandler mismatch: got %q want %q", out, want)
	}
}

func TestGetClassAndFileFuncs(t *testing.T) {
	out := runPHP(t, `<?php
class Foo { public $x = 1; public function bar() { return 2; } }
$o = new Foo();
$r = [];
$r[] = class_exists("Foo") ? 1 : 0;
$r[] = method_exists($o, "bar") ? 1 : 0;
$r[] = property_exists($o, "x") ? 1 : 0;
$r[] = get_class($o);
$r[] = is_a($o, "Foo") ? 1 : 0;
echo implode("|", $r);
echo "|" . implode(",", get_class_methods("Foo"));
`)
	want := "1|1|1|Foo|1|bar"
	if out != want {
		t.Errorf("oo reflect mismatch: got %q want %q", out, want)
	}
}

func TestCsvIniAndFileFuncs(t *testing.T) {
	out := runPHP(t, `<?php
$r = [];
$r[] = json_encode(str_getcsv("a,b,\"c,d\""));
$r[] = json_encode(str_getcsv("1;2;3", ";"));
$ini = parse_ini_string("k=v\n[sec]\na=1\nb=two", true, INI_SCANNER_TYPED);
$r[] = $ini["k"];
$r[] = $ini["sec"]["a"];
$r[] = $ini["sec"]["b"];
$r[] = is_string(getcwd()) ? 1 : 0;
$r[] = implode(",", hash_algos());
echo implode("|", $r);
`)
	want := `["a","b","c,d"]|["1","2","3"]|v|1|two|1|md5,sha1,sha256`
	if out != want {
		t.Errorf("csv/ini/file mismatch: got %q want %q", out, want)
	}
}
