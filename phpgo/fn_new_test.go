package phpgo

import (
	"testing"
)

// runPHP 执行 PHP 源码并返回 echo 输出
func runPHP(t *testing.T, src string) string {
	t.Helper()
	env, err := Execute(src, nil, nil)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	return env.EchoOutput()
}

func TestNewStrFuncs(t *testing.T) {
	out := runPHP(t, `<?php
echo "cmp=".strcmp("a","b")."|".strcmp("b","a")."|".strcmp("a","a")."\n";
echo "casecmp=".strcasecmp("Ab","aB")."\n";
echo "ncmp=".strncmp("abcde","abcXY",3)."\n";
echo "natcmp=".strnatcmp("img2","img10")."\n";
echo "rev=".strrev("hello")."\n";
echo "rot13=".str_rot13("hello")."\n";
echo "cnt=".substr_count("aaaXaaa","aa")."\n";
echo "srepl=".substr_replace("abcdef","X",2,3)."\n";
echo "tags=".strip_tags("<b>hi</b><script>alert(1)</script>world")."\n";
echo "words=".str_word_count("hello world foo")."\n";
printf("printf=%s-%d\n","x",42);
echo "vs=".vsprintf("%s-%d", array("y",7))."\n";
echo "ent=".htmlentities("<a href=\"x\">&é</a>")."\n";
echo "dec=".html_entity_decode("&lt;a&gt;&amp;&#233;&#x41;")."\n";
`)
	expectContains(t, out, "cmp=-1|1|0")
	expectContains(t, out, "casecmp=0")
	expectContains(t, out, "ncmp=0")
	expectContains(t, out, "natcmp=-1")
	expectContains(t, out, "rev=olleh")
	expectContains(t, out, "rot13=uryyb")
	expectContains(t, out, "cnt=2")
	expectContains(t, out, "srepl=abXf")
	expectContains(t, out, "tags=hiworld")
	expectContains(t, out, "words=3")
	expectContains(t, out, "printf=x-42")
	expectContains(t, out, "vs=y-7")
	expectContains(t, out, "dec=<a>&éA")
	t.Logf("str out:\n%s", out)
}

func TestNewArrFuncs(t *testing.T) {
	out := runPHP(t, `<?php
$a = array(1,2,3,2,4);
echo "diff=".json_encode(array_diff($a, array(2,4)))."\n";
echo "inter=".json_encode(array_intersect($a, array(2,3)))."\n";
echo "dk=".json_encode(array_diff_key(array("a"=>1,"b"=>2), array("b"=>9)))."\n";
echo "ik=".json_encode(array_intersect_key(array("a"=>1,"b"=>2), array("a"=>9,"c"=>1)))."\n";
echo "chunk=".json_encode(array_chunk($a, 2))."\n";
echo "range=".json_encode(range(1,5))."\n";
echo "range2=".json_encode(range("a","d"))."\n";
echo "fk=".json_encode(array_fill_keys(array("x","y"), 0))."\n";
echo "pad=".json_encode(array_pad(array(1), 4, 9))."\n";
echo "cv=".json_encode(array_count_values($a))."\n";
echo "prod=".array_product(array(2,3,4))."\n";
echo "first=".array_key_first(array("b"=>2,"a"=>1))."|".array_key_last(array("b"=>2,"a"=>1))."\n";
$s = array(1,2,3,4,5);
echo "splice=".json_encode(array_splice($s, 1, 2, array(8,9)))." rest=".json_encode($s)."\n";
$sh = array(1,2,3,4);
shuffle($sh);
echo "shuf_len=".count($sh)."\n";
echo "reduce=".array_reduce(array(1,2,3,4), "myadd", 0)."\n";
function myadd($c,$v){ return $c+$v; }
echo "mr=".json_encode(array_merge_recursive(array("a"=>array(1,2),"n"=>3), array("a"=>array(4),"m"=>5)))."\n";
$mr2 = array_merge_recursive(array("a"=>array(1,2),"n"=>3), array("a"=>array(4),"m"=>5));
echo "mra=".json_encode($mr2["a"])."|mrn=".$mr2["n"]."|mrm=".$mr2["m"]."\n";
`)
	expectContains(t, out, `diff={"0":1,"2":3}`)
	expectContains(t, out, `inter={"1":2,"2":3,"3":2}`)
	expectContains(t, out, `dk={"a":1}`)
	expectContains(t, out, `ik={"a":1}`)
	expectContains(t, out, `chunk=[[1,2],[3,2],[4]]`)
	expectContains(t, out, `range=[1,2,3,4,5]`)
	expectContains(t, out, `range2=["a","b","c","d"]`)
	expectContains(t, out, `fk={"x":0,"y":0}`)
	expectContains(t, out, `pad=[1,9,9,9]`)
	expectContains(t, out, `cv={"1":1,"2":2,"3":1,"4":1}`)
	expectContains(t, out, `prod=24`)
	expectContains(t, out, `first=b|a`)
	expectContains(t, out, `splice=[2,3]`)
	expectContains(t, out, `rest=[1,8,9,4,5]`)
	expectContains(t, out, `shuf_len=4`)
	expectContains(t, out, "reduce=10")
	expectContains(t, out, `mr={"a":[1,2,4],"n":3,"m":5}`)
	expectContains(t, out, "mra=[1,2,4]|mrn=3|mrm=5")
	t.Logf("arr out:\n%s", out)
}

func TestNewSortFuncs(t *testing.T) {
	out := runPHP(t, `<?php
function od($arr){ $s=""; foreach($arr as $k=>$v){ $s.=$k."=>".$v.";"; } return $s; }
$a = array("b"=>2,"a"=>1,"c"=>3);
arsort($a);
echo "arsort=".od($a)."\n";
$b = array("b"=>2,"a"=>1,"c"=>3);
krsort($b);
echo "krsort=".od($b)."\n";
$c = array(3,1,2);
usort($c, "mycmp");
echo "usort=".od($c)."\n";
function mycmp($x,$y){ return $x > $y ? 1 : ($x < $y ? -1 : 0); }
$d = array("x"=>3,"y"=>1,"z"=>2);
uasort($d, "mycmp");
echo "uasort=".od($d)."\n";
$e = array("b"=>1,"a"=>2,"c"=>3);
uksort($e, "mycmpk");
echo "uksort=".od($e)."\n";
function mycmpk($x,$y){ return $x > $y ? 1 : ($x < $y ? -1 : 0); }
// 验证原有 sort 现在真正修改原数组
$f = array(3,1,2);
sort($f);
echo "sort_fix=".json_encode($f)."\n";
`)
	expectContains(t, out, "arsort=c=>3;b=>2;a=>1;")
	expectContains(t, out, "krsort=c=>3;b=>2;a=>1;")
	expectContains(t, out, "usort=0=>1;1=>2;2=>3;")
	expectContains(t, out, "uasort=y=>1;z=>2;x=>3;")
	expectContains(t, out, "uksort=a=>2;b=>1;c=>3;")
	expectContains(t, out, "sort_fix=[1,2,3]")
	t.Logf("sort out:\n%s", out)
}

func TestNewVarCallableFuncs(t *testing.T) {
	out := runPHP(t, `<?php
class Foo { public $x = 1; }
echo "isobj=".is_object(new Foo())."|".is_object(5)."\n";
echo "isscalar=".is_scalar(1)."|".is_scalar(array())."\n";
$v = "123";
settype($v, "int");
echo "settype=".gettype($v)."\n";
echo "call=".call_user_func("myfunc", 3, 4)."\n";
echo "callarr=".call_user_func_array("myfunc", array(5,6))."\n";
function myfunc($a,$b){ return $a*$b; }
echo "fnex=".function_exists("myfunc")."|".function_exists("strlen")."|".function_exists("nope")."\n";
define("MY_CONST", 42);
echo "defined=".defined("MY_CONST")."|".defined("NOPE_CONST")."\n";
echo "const=".constant("MY_CONST")."\n";
$src = array("foo"=>"bar","num"=>7);
extract($src);
echo "extract=".$foo.$num."\n";
`)
	expectContains(t, out, "isobj=1|")
	expectContains(t, out, "isscalar=1|")
	expectContains(t, out, "settype=integer")
	expectContains(t, out, "call=12")
	expectContains(t, out, "callarr=30")
	expectContains(t, out, "fnex=1|1|")
	expectContains(t, out, "defined=1|")
	expectContains(t, out, "const=42")
	expectContains(t, out, "extract=bar7")
	t.Logf("var out:\n%s", out)
}

func TestJSONKeyOrder(t *testing.T) {
	out := runPHP(t, `<?php
// 关联数组按插入顺序输出（此前会被 Go map 排序打乱）
echo "order=".json_encode(array("b"=>2,"a"=>1,"c"=>3))."\n";
// 嵌套数组同样保序
echo "nest=".json_encode(array("z"=>array("y"=>1,"x"=>2),"m"=>3))."\n";
// 连续数字键仍输出 JSON 数组
echo "seq=".json_encode(array(5,6,7))."\n";
// 非 0 起始数字键仍输出对象
echo "shift=".json_encode(array(2=>"a", 5=>"b"))."\n";
// 空数组
echo "empty=".json_encode(array())."\n";
// JSON_UNESCAPED_UNICODE 保留中文
echo "uni=".json_encode(array("k"=>"中文"), 256)."\n";
// JSON_PRETTY_PRINT
echo "pp=".json_encode(array("a"=>1,"b"=>2), 128)."\n";
`)
	expectContains(t, out, `order={"b":2,"a":1,"c":3}`)
	expectContains(t, out, `nest={"z":{"y":1,"x":2},"m":3}`)
	expectContains(t, out, `seq=[5,6,7]`)
	expectContains(t, out, `shift={"2":"a","5":"b"}`)
	expectContains(t, out, "empty=[]")
	expectContains(t, out, `uni={"k":"中文"}`)
	expectContains(t, out, "{\n    \"a\": 1,\n    \"b\": 2\n  }")
	t.Logf("json out:\n%s", out)
}

func TestNewMathDatePreg(t *testing.T) {
	out := runPHP(t, `<?php
echo "exp=".round(exp(1), 2)."\n";
echo "log=".round(log(100,10), 2)."\n";
echo "log10=".round(log10(1000), 2)."\n";
echo "log2=".round(log2(8), 2)."\n";
echo "fmod=".fmod(5.5, 2)."\n";
echo "rad=".round(deg2rad(180), 2)."\n";
echo "deg=".round(rad2deg(3.14159), 2)."\n";
echo "sin=".round(sin(0), 2)."|cos=".round(cos(0),2)."\n";
echo "oct=".decoct(8)."|".octdec("10")."\n";
echo "fin=".is_finite(1.0)."|".is_nan(log(-1))."\n";
echo "chk=".checkdate(2,29,2024)."|".checkdate(2,29,2023)."\n";
echo "gd=".json_encode(getdate(mktime(0,0,0,1,15,2020)))."\n";
echo "mk=".date("Y-m-d", mktime(0,0,0,2,3,2021))."\n";
echo "gmmk=".gmdate("Y-m-d", gmmktime(0,0,0,2,3,2021))."\n";
echo "quote=".preg_quote("a.b*c")."\n";
$arr = array("apple", "banana", "apricot");
echo "grep=".json_encode(preg_grep("/^ap/", $arr))."\n";
`)
	expectContains(t, out, "exp=2.72")
	expectContains(t, out, "log=2")
	expectContains(t, out, "log10=3")
	expectContains(t, out, "log2=3")
	expectContains(t, out, "fmod=1.5")
	expectContains(t, out, "rad=3.14")
	expectContains(t, out, "deg=180")
	expectContains(t, out, "sin=0|cos=1")
	expectContains(t, out, "oct=10|8")
	expectContains(t, out, "fin=1|1")
	expectContains(t, out, "chk=1|")
	expectContains(t, out, `"year":2020`)
	expectContains(t, out, "mk=2021-02-03")
	expectContains(t, out, "gmmk=2021-02-03")
	expectContains(t, out, `quote=a\.b\*c`)
	expectContains(t, out, `grep={"0":"apple","2":"apricot"}`)
	t.Logf("math out:\n%s", out)
}

func expectContains(t *testing.T, out, sub string) {
	t.Helper()
	if !containsStr(out, sub) {
		t.Errorf("output missing %q\n---\n%s", sub, out)
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
