package phpgo

import "testing"

// TestNestedPushAssign 验证 $arr[$key][] = $val（带键的追加赋值）能正确分组而非退化成 $arr[] = $val。
func TestNestedPushAssign(t *testing.T) {
	src := `<?php
$grouped = [];
$items = [
    ['group'=>'香港', 'name'=>'翡翠台'],
    ['group'=>'香港', 'name'=>'J2'],
    ['group'=>'央视', 'name'=>'CCTV1'],
    ['group'=>'体育', 'name'=>'NBA'],
];
foreach ($items as $chan) {
    $grouped[$chan['group']][] = $chan;
}
// 输出分组键与每组的成员数
$cnt = 0;
foreach ($grouped as $g => $list) {
    echo "G=$g N=" . count($list) . "\n";
    $cnt++;
}
echo "GROUPS=$cnt\n";
`
	env, err := Execute(src, nil, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	got := env.EchoOutput()
	want := "G=香港 N=2\nG=央视 N=1\nG=体育 N=1\nGROUPS=3\n"
	if got != want {
		t.Fatalf("nested push assign failed:\n got=%q\nwant=%q", got, want)
	}
}

// TestPlainPushStillWorks 验证纯 $arr[] = $val 追加赋值不受影响。
func TestPlainPushStillWorks(t *testing.T) {
	src := `<?php
$a = [];
$a[] = 'x';
$a[] = 'y';
echo "LEN=" . count($a) . "\n";
`
	env, err := Execute(src, nil, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if got := env.EchoOutput(); got != "LEN=2\n" {
		t.Fatalf("plain push failed: got=%q want=%q", got, "LEN=2\n")
	}
}
