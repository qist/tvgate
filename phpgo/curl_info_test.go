package phpgo

import "testing"

// 脚本里最常见的 curl 用法：
//
//	$resp = curl_exec($ch);                     // CURLOPT_HEADER=true → 响应头 + 空行 + 正文
//	$hs   = curl_getinfo($ch, CURLINFO_HEADER_SIZE);
//	$body = substr($resp, $hs);                 // 剥离响应头
//	json_decode($body)
//
// 之前缺 CURLINFO_HEADER_SIZE，$hs 为 null → substr 不剥离 → json_decode 必然 Syntax error。
func TestCurlHeaderSizeSplitBody(t *testing.T) {
	proxy := func(method, url string, opts *CurlOptions) (*ProxyResult, error) {
		return &ProxyResult{
			StatusCode:   200,
			Body:         `{"result":"tvgate"}`,
			Headers:      []string{"Content-Type: application/json", "Content-Length: 19"},
			ContentType:  "application/json",
			EffectiveURL: url,
		}, nil
	}

	out, err := Execute(`<?php
$ch = curl_init();
curl_setopt_array($ch, [
    CURLOPT_URL => "http://example.test/api",
    CURLOPT_POST => true,
    CURLOPT_POSTFIELDS => "a=1",
    CURLOPT_RETURNTRANSFER => true,
    CURLOPT_HEADER => true,
    CURLOPT_TIMEOUT => 5,
]);
$resp = curl_exec($ch);
$hs = curl_getinfo($ch, CURLINFO_HEADER_SIZE);
$body = substr($resp, $hs);
$r = json_decode($body, true);
echo "hs=" . $hs . "|body=" . $body . "|r=" . $r["result"] . "|size=" . curl_getinfo($ch, CURLINFO_SIZE_DOWNLOAD) . "|code=" . curl_getinfo($ch, CURLINFO_HTTP_CODE);
`, proxy, nil)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}

	echo := out.EchoOutput()
	if echo == "" {
		t.Fatal("无输出")
	}
	// 必须能从“响应头 + 正文”里精确切出 JSON 正文
	if !containsAll(echo, "|body={\"result\":\"tvgate\"}|r=tvgate|size=19|code=200") {
		t.Fatalf("CURLINFO_HEADER_SIZE 切分不正确: %s", echo)
	}
	if containsAll(echo, "hs=0|") {
		t.Fatalf("CURLINFO_HEADER_SIZE 不应为 0: %s", echo)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func TestCurlInfoKey(t *testing.T) {
	cases := map[string]string{
		"2097163":              "header_size",
		"CURLINFO_HEADER_SIZE": "header_size",
		"2097154":              "http_code",
		"CURLINFO_HTTP_CODE":   "http_code",
		"3145736":              "size_download",
		"3145731":              "total_time",
		"CURLINFO_UNKNOWN_X":   "",
	}
	for in, want := range cases {
		if got := curlInfoKey(in); got != want {
			t.Errorf("curlInfoKey(%q) = %q, want %q", in, got, want)
		}
	}
}
