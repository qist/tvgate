package phpgo

import (
	"strings"
	"testing"
)

func TestSerializeBasic(t *testing.T) {
	out := runPHP(t, `<?php
echo serialize(null)."\n";
echo serialize(true)."|".serialize(false)."\n";
echo serialize(42)."|".serialize(-7)."|".serialize(0)."\n";
echo serialize(1.5)."|".serialize(2.0)."\n";
echo serialize("ab")."|".serialize("")."|".serialize("中文")."\n";
echo serialize(array())."\n";
echo serialize(array(1,2))."\n";
echo serialize(array("a"=>1,"b"=>"x"))."\n";
echo serialize(array("z"=>array(1,array(2))))."\n";
$x = array();
$x[5] = "v";
$x["k"] = 1;
echo serialize($x)."\n";
$o = (object) array("a"=>1,"b"=>"x");
echo serialize($o)."\n";
`)
	expectContains(t, out, "N;")
	expectContains(t, out, "b:1;|b:0;")
	expectContains(t, out, "i:42;|i:-7;|i:0;")
	expectContains(t, out, "d:1.5;|d:2;")
	expectContains(t, out, `s:2:"ab";|s:0:"";|s:6:"中文";`)
	expectContains(t, out, "a:0:{}")
	expectContains(t, out, "a:2:{i:0;i:1;i:1;i:2;}")
	expectContains(t, out, `a:2:{s:1:"a";i:1;s:1:"b";s:1:"x";}`)
	expectContains(t, out, `a:1:{s:1:"z";a:2:{i:0;i:1;i:1;a:1:{i:0;i:2;}}}`)
	expectContains(t, out, `a:2:{i:5;s:1:"v";s:1:"k";i:1;}`)
	expectContains(t, out, `O:8:"stdClass":2:{s:1:"a";i:1;s:1:"b";s:1:"x";}`)
	t.Logf("serialize out:\n%s", out)
}

func TestUnserializeRoundTrip(t *testing.T) {
	out := runPHP(t, `<?php
$a = array("name"=>"bob", "tags"=>array("x","y"), "n"=>7, "f"=>1.25, "b"=>true, "z"=>null);
$s = serialize($a);
$u = unserialize($s);
echo "same=".(serialize($u) === $s ? "1" : "0")."\n";
echo "json=".json_encode($u)."\n";
echo "name=".$u["name"]."|n=".$u["n"]."|f=".$u["f"]."\n";
echo "tags=".json_encode($u["tags"])."\n";
echo "null=".is_null($u["z"])."|bool=".$u["b"]."\n";
// 外部（原生 PHP）产出的固定载荷
$p = unserialize('a:2:{s:1:"k";i:9;s:1:"j";a:1:{i:0;s:2:"hi";}}');
echo "p=".json_encode($p)."\n";
$o = unserialize('O:8:"stdClass":1:{s:1:"q";i:5;}');
echo "obj=".$o->q."|".is_object($o)."\n";
echo "i=".unserialize("i:-3;")."\n";
echo "d=".unserialize("d:0.5;")."\n";
echo "nil=".is_null(unserialize("N;"))."\n";
// R: 引用标记（PHP 递归结构会产出）
$r = unserialize('a:2:{i:0;a:1:{i:0;i:7;}i:1;R:2;}');
echo "ref=".json_encode($r)."\n";
`)
	expectContains(t, out, "same=1")
	expectContains(t, out, `json={"name":"bob","tags":["x","y"],"n":7,"f":1.25,"b":true,"z":null}`)
	expectContains(t, out, "name=bob|n=7|f=1.25")
	expectContains(t, out, `tags=["x","y"]`)
	expectContains(t, out, "null=1|bool=1")
	expectContains(t, out, `p={"k":9,"j":["hi"]}`)
	expectContains(t, out, "obj=5|1")
	expectContains(t, out, "i=-3")
	expectContains(t, out, "d=0.5")
	expectContains(t, out, "nil=1")
	expectContains(t, out, `ref=[[7],[7]]`)
	t.Logf("unserialize out:\n%s", out)
}

// 用户示例脚本（serialize → print_r 往返）逐字节比对
func TestUserExampleRoundTrip(t *testing.T) {
	out := runPHP(t, `<?php

// 要序列化的数据
$data = array(
    'name' => 'qist',
    'age' => 18,
    'channels' => array(
        'cctv1',
        'cctv5',
        'cctv6'
    )
);

// 序列化
$serialized = serialize($data);

echo "serialize:\n";
echo $serialized . "\n\n";

// 反序列化
$unserialized = unserialize($serialized);

echo "unserialize:\n";
print_r($unserialized);
`)
	expectContains(t, out, "serialize:\n"+`a:3:{s:4:"name";s:4:"qist";s:3:"age";i:18;s:8:"channels";a:3:{i:0;s:5:"cctv1";i:1;s:5:"cctv5";i:2;s:5:"cctv6";}}`)
	expectContains(t, out, "[name] => qist")
	expectContains(t, out, "[age] => 18")
	expectContains(t, out, "[channels] => Array")
	expectContains(t, out, "[0] => cctv1")
	expectContains(t, out, "[2] => cctv6")
}

// 通过 URL 参数传递（$_GET）
func TestUnserializeFromGet(t *testing.T) {
	src := `<?php

if (isset($_GET['data'])) {
    $data = unserialize($_GET['data']);

    if ($data !== false) {
        print_r($data);
    }
}

// 示例
$data = array(
    'id' => 123,
    'name' => 'test'
);

echo urlencode(serialize($data));
`
	// 无 data 参数：只输出示例的 urlencode 结果
	bare := runPHP(t, src)
	expectContains(t, bare, `a%3A2%3A%7Bs%3A2%3A%22id%22%3Bi%3A123%3Bs%3A4%3A%22name%22%3Bs%3A4%3A%22test%22%3B%7D`)

	// 带 data 参数：先 print_r 还原出的数组
	env, err := Execute(src, nil, func(e *Env) {
		e.get = map[string]string{"data": `a:2:{s:2:"id";i:123;s:4:"name";s:4:"test";}`}
	})
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	out := env.EchoOutput()
	expectContains(t, out, "[id] => 123")
	expectContains(t, out, "[name] => test")
}

func TestUnserializeOptions(t *testing.T) {
	out := runPHP(t, `<?php
$str = 'O:3:"Foo":2:{s:1:"a";i:1;s:1:"b";s:1:"x";}';

// 默认允许所有类
$obj = unserialize($str);
echo "default=".gettype($obj)."|".$obj->a."\n";

// allowed_classes => false：降级为 __PHP_Incomplete_Class，原类名保留
$safe = unserialize($str, array('allowed_classes' => false));
echo "safe=".gettype($safe)."|".$safe->__PHP_Incomplete_Class_Name."|".$safe->a."\n";

// 白名单命中（类名大小写不敏感）
echo "hit=".gettype(unserialize($str, array('allowed_classes' => array('foo'))))."\n";
// 白名单未命中
$miss = unserialize($str, array('allowed_classes' => array('Bar')));
echo "miss=".$miss->__PHP_Incomplete_Class_Name."\n";
// __PHP_Incomplete_Class_Name 需作为首个属性输出（回归：曾漏写 PropKeys 顺序）
$safe2 = unserialize($str, array('allowed_classes' => false));
echo "safe_ser=".serialize($safe2)."\n";

// max_depth：只对数组/对象嵌套计数
echo "depth_ok=".gettype(unserialize('a:1:{i:0;a:1:{i:0;i:1;}}', array('max_depth' => 2)))."\n";
echo "depth_over=".var_export(unserialize('a:1:{i:0;a:1:{i:0;i:1;}}', array('max_depth' => 1)), true)."\n";
`)
	expectContains(t, out, "default=object|1")
	expectContains(t, out, "safe=object|Foo|1")
	expectContains(t, out, "hit=object")
	expectContains(t, out, "miss=Foo")
	expectContains(t, out, "depth_ok=array")
	expectContains(t, out, "depth_over=false")
	expectContains(t, out, `safe_ser=O:22:"__PHP_Incomplete_Class":3:{s:27:"__PHP_Incomplete_Class_Name";s:3:"Foo";s:1:"a";i:1;s:1:"b";s:1:"x";}`)
	t.Logf("options out:\n%s", out)
}

// 对象属性按声明/插入顺序输出（PHP 语义，非排序）
func TestSerializeObjectOrder(t *testing.T) {
	out := runPHP(t, `<?php
$o = (object) array("z"=>1,"a"=>2,"m"=>3);
echo serialize($o)."\n";
echo json_encode($o)."\n";
$o->b = 4;
echo serialize($o)."\n";
`)
	expectContains(t, out, `O:8:"stdClass":3:{s:1:"z";i:1;s:1:"a";i:2;s:1:"m";i:3;}`)
	expectContains(t, out, `{"z":1,"a":2,"m":3}`)
	expectContains(t, out, `O:8:"stdClass":4:{s:1:"z";i:1;s:1:"a";i:2;s:1:"m";i:3;s:1:"b";i:4;}`)
	t.Logf("obj order out:\n%s", out)
}

// R:/r: 引用标记：重复出现的同一 zval 必须输出引用，不能丢数据
func TestSerializeReferences(t *testing.T) {
	out := runPHP(t, `<?php
// 同一数组出现两次：PHP 输出 R:<id>;（id 按遇到顺序分配：外层=1、内层=2）
$inner = array(1,2);
echo serialize(array($inner, $inner))."\n";
// 循环引用：$a[] = &$a 必须输出 R:1;（不能丢数据，也不能死循环）
$a = array(1);
$a[] = &$a;
echo serialize($a)."\n";
// 外部 R: 载荷可正常还原（R:2 指向内层数组 [1,2]）
echo json_encode(unserialize('a:2:{i:0;a:2:{i:0;i:1;i:1;i:2;}i:1;R:2;}'))."\n";
`)
	expectContains(t, out, "a:2:{i:0;a:2:{i:0;i:1;i:1;i:2;}i:1;R:2;}")
	expectContains(t, out, "a:2:{i:0;i:1;i:1;R:1;}")
	expectContains(t, out, "[[1,2],[1,2]]")
	t.Logf("ref out:\n%s", out)
}

// 循环引用（phpgo 解析器尚不支持 $a[] = &$a，这里在 Go 层直接构造自引用数组）：
// 必须输出 R:1; 终止递归，而不是退化成 N; 丢数据，也不能死循环
func TestSerializeCycle(t *testing.T) {
	a := NewArray()
	a.ArraySet(NewInt(0), NewInt(1))
	a.ArraySet(NewInt(1), a) // 自引用：共享同一底层 map
	if got := phpSerialize(a); got != "a:2:{i:0;i:1;i:1;R:1;}" {
		t.Errorf("serialize(cycle) = %q, want %q", got, "a:2:{i:0;i:1;i:1;R:1;}")
	}
}

// 回归：反序列化含 R: 的循环结构后赋值/读取不得崩溃
// （Clone 曾对自引用数组无限递归导致栈溢出）
func TestUnserializeCycleNoCrash(t *testing.T) {
	out := runPHP(t, `<?php
$a = array(1);
$a[] = &$a;
$s = serialize($a);
echo "s=".$s."\n";
$u = unserialize($s);
echo "u0=".$u[0]."\n";
$deep = $u; // Clone 自引用数组
echo "deep0=".$deep[0]."\n";
`)
	expectContains(t, out, "s=a:2:{i:0;i:1;i:1;R:1;}")
	expectContains(t, out, "u0=1")
	expectContains(t, out, "deep0=1")
	t.Logf("cycle rt out:\n%s", out)
}

// 深嵌套数组 serialize 不应被截断（此前深度 guard 512 会误伤 >512 层）
func TestSerializeDeepNested(t *testing.T) {
	v := NewInt(1)
	for i := 0; i < 600; i++ {
		inner := NewArray()
		inner.ArraySet(NewInt(0), v)
		v = inner
	}
	s := phpSerialize(v)
	if got := strings.Count(s, "a:1:{"); got != 600 {
		t.Fatalf("deep serialize 被截断: 期望 600 层, 实际 %d", got)
	}
}

func TestUnserializeErrors(t *testing.T) {
	out := runPHP(t, `<?php
// 解析失败时与 PHP 一致返回 false
echo "bad1=".var_export(unserialize("garbage"), true)."\n";
echo "bad2=".var_export(unserialize(""), true)."\n";
echo "bad3=".var_export(unserialize("a:2:{i:0;}"), true)."\n";
echo "bad4=".var_export(unserialize('s:10:"short";'), true)."\n";
echo "bad5=".var_export(unserialize("i:1;trailing"), true)."\n";
// b:0; 合法且值为 false
echo "ok=".var_export(unserialize("b:0;"), true)."\n";
`)
	expectContains(t, out, "bad1=false")
	expectContains(t, out, "bad2=false")
	expectContains(t, out, "bad3=false")
	expectContains(t, out, "bad4=false")
	expectContains(t, out, "bad5=false")
	expectContains(t, out, "ok=false")
	t.Logf("unserialize err out:\n%s", out)
}
