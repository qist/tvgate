package phpgo

import (
	"testing"
)

// TestDateTimeClass：内置 DateTime 最小实现，覆盖 akmg.php 回看分支的用法
// （new DateTime('YmdHis') → modify('-8 hours') → format('YmdHis')）。
func TestDateTimeClass(t *testing.T) {
	out := runPHP(t, `<?php
date_default_timezone_set('UTC');
$d = new DateTime('20260904200000');
$d->modify('-8 hours');
echo $d->format('YmdHis') . "\n";
$d2 = new DateTime('2026-09-04 20:00:00');
$d2->modify('+1 day');
echo $d2->format('Y-m-d H:i:s') . "\n";
$d3 = new DateTime('20260904200000');
echo $d3->getTimestamp() . "\n";
$d3->setTimestamp(1788000000);
echo $d3->format('U') . "\n";
$d4 = new DateTime();
echo ($d4->getTimestamp() > 0 ? 'now-ok' : 'now-bad') . "\n";
$bad = new DateTime('not-a-date');
echo ($bad->format('YmdHis') === false ? 'parse-fail' : 'parse-ok') . "\n";
`)
	expectContains(t, out, "20260904120000")
	expectContains(t, out, "2026-09-05 20:00:00")
	// 20260904200000 UTC = 1788552000
	expectContains(t, out, "1788552000")
	expectContains(t, out, "1788000000")
	expectContains(t, out, "now-ok")
	expectContains(t, out, "parse-fail")
}

// TestDateTimeMultiPartModify：modify 支持多段相对表达式。
func TestDateTimeMultiPartModify(t *testing.T) {
	out := runPHP(t, `<?php
date_default_timezone_set('UTC');
$d = new DateTime('20260904200000');
$d->modify('+1 day -2 hours');
echo $d->format('YmdHis') . "\n";
`)
	expectContains(t, out, "20260905180000")
}
