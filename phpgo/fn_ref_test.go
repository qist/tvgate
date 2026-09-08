package phpgo

import (
	"testing"
)

// & 引用语法：$b = &$a / &$arr[$k] / &$obj->prop
func TestRefSyntax(t *testing.T) {
	out := runPHP(t, `<?php
// 变量别名（双向写穿）
$x = 1;
$y = &$x;
$y = 5;
echo "alias=".$x."|".$y."\n";
$x = 7;
echo "alias2=".$x."|".$y."\n";

// 数组元素引用写穿
$arr = array(10, 20);
$r = &$arr[1];
$r = 99;
echo "idx=".json_encode($arr)."\n";

// 对象属性引用写穿
$o = (object) array("a"=>1);
$p = &$o->a;
$p = 42;
echo "prop=".$o->a."\n";

// 数组整体别名
$a = array("k"=>1);
$b = &$a;
$b["k"] = 5;
echo "arr=".$a["k"]."|".$b["k"]."\n";

// 回归：通过别名加键/删键必须反映到原数组（否则 serialize/json_encode 丢键）
$a2 = array("x"=>1);
$b2 = &$a2;
$b2["k"] = 5;
echo "alias_add=".json_encode($a2)."|".serialize($a2)."\n";
unset($b2["x"]);
echo "alias_del=".json_encode($a2)."\n";
`)
	expectContains(t, out, "alias=5|5")
	expectContains(t, out, "alias2=7|7")
	expectContains(t, out, "idx=[10,99]")
	expectContains(t, out, "prop=42")
	expectContains(t, out, "arr=5|5")
	expectContains(t, out, `alias_add={"x":1,"k":5}|a:2:{s:1:"x";i:1;s:1:"k";i:5;}`)
	expectContains(t, out, `alias_del={"k":5}`)
	t.Logf("ref out:\n%s", out)
}

// 用户函数 by-ref 形参：function f(&$n) 写穿到调用方实参
func TestUserFuncByRefParam(t *testing.T) {
	out := runPHP(t, `<?php
// 1) 变量形参（现代写法：形参声明 &，调用不带 &）
function bump(&$n) { $n = $n + 1; }
$x = 10; bump($x);
echo "v=".$x."\n";

// 2) by-ref 与 by-val 混合：只写穿 by-ref
function mix(&$q, $z) { $q = 100; $z = 99; }
$y = 1; $w = 2; mix($y, $w);
echo "m=".$y."|".$w."\n";

// 3) foreach ($arr as &$v) 的元素作为实参透传
$arr = array(1,2,3);
foreach ($arr as &$v) { bump($v); }
unset($v);
echo "each=".json_encode($arr)."\n";

// 4) 数组元素作为实参
$m = array(10,20);
function inc1(&$v) { $v = $v + 1; }
inc1($m[1]);
echo "elem=".json_encode($m)."\n";

// 5) 对象属性作为实参
class Counter { public $n = 5; }
$c = new Counter();
inc1($c->n);
echo "prop=".$c->n."\n";

// 6) 方法 by-ref 形参
class Calc { function apply(&$v, $d) { $v = $v + $d; } }
$c2 = new Calc();
$t = 1;
$c2->apply($t, 41);
echo "meth=".$t."\n";

// 7) 嵌套：by-ref 函数再调用 by-ref 函数（引用须直达最外层调用方）
function outer(&$a) { inner($a); }
function inner(&$b) { $b = $b * 2; }
$k = 21; outer($k);
echo "nest=".$k."\n";

// 8) 函数内把 by-ref 参数交给 sort 等内置原地函数
function sorter(&$list) { sort($list); }
$u = array(3,1,2);
sorter($u);
echo "sort=".json_encode($u)."\n";

// 9) 自增/自减/复合赋值写穿
$zz = 5;
function incr(&$n) { $n++; }
incr($zz);
echo "inc=".$zz."\n";
function decr(&$n) { $n--; }
decr($zz);
echo "dec=".$zz."\n";
$cc = 4;
function cadd(&$n) { $n += 10; }
cadd($cc);
echo "cadd=".$cc."\n";

// 10) 字面量传给 by-ref：退化为按值，不崩溃
function nop2(&$p) { $p = 7; }
$r = nop2(5);
echo "lit=".var_export($r, true)."\n";
`)
	expectContains(t, out, "v=11")
	expectContains(t, out, "m=100|2")
	expectContains(t, out, "each=[2,3,4]")
	expectContains(t, out, "elem=[10,21]")
	expectContains(t, out, "prop=6")
	expectContains(t, out, "meth=42")
	expectContains(t, out, "nest=42")
	expectContains(t, out, "sort=[1,2,3]")
	expectContains(t, out, "inc=6")
	expectContains(t, out, "dec=5")
	expectContains(t, out, "cadd=14")
	expectContains(t, out, "lit=NULL")
	t.Logf("byref out:\n%s", out)
}

// 回归：Clone 自引用/循环数组不得爆栈（此前 Value.Clone 无限递归）
func TestCloneCycle(t *testing.T) {
	a := NewArray()
	a.ArraySet(NewInt(0), NewInt(1))
	a.ArraySet(NewInt(1), a) // 自引用
	c := a.Clone()
	if c.Kind != KindArray || c.ArrayGet(NewInt(0)).ToInt() != 1 {
		t.Fatalf("clone(cycle) 结果异常: %#v", c)
	}
}

// foreach 引用：foreach ($arr as &$v) / foreach ($arr as $k => &$v)
func TestForeachByRef(t *testing.T) {
	out := runPHP(t, `<?php
// foreach ($arr as &$v)
$nums = array(1,2,3);
foreach ($nums as &$n) { $n = $n * 10; }
unset($n);
echo "nums=".json_encode($nums)."\n";

// foreach ($arr as $k => &$v)
$m = array("a"=>1, "b"=>2);
foreach ($m as $k => &$v) { $v = $v + 100; }
unset($v);
echo "map=".json_encode($m)."\n";

// 非引用 foreach 不受影响（按值拷贝）
$orig = array(1,2);
foreach ($orig as $o) { $o = 99; }
echo "orig=".json_encode($orig)."\n";
`)
	expectContains(t, out, "nums=[10,20,30]")
	expectContains(t, out, `map={"a":101,"b":102}`)
	expectContains(t, out, "orig=[1,2]")
	t.Logf("foreach ref out:\n%s", out)
}

// & 作为二元按位与不能被引用语法吃掉
func TestBitwiseAndUnaffected(t *testing.T) {
	out := runPHP(t, `<?php
echo "band=".(6 & 3)."|".(5 & 4)."|".(1 & 1)."\n";
`)
	expectContains(t, out, "band=2|4|1")
}
