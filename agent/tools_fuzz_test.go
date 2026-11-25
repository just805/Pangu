package agent

import (
	"strings"
	"testing"
)

func TestFuzzCommandAPI(t *testing.T) {
	cmd, warnings, err := fuzzCommand(fuzzPlanInput{
		Mode:     "api",
		Target:   "https://api.example.com",
		Contract: "https://api.example.com/openapi.json",
		Requests: 120,
		Headers:  []string{"Authorization: Bearer abc", "X-Probe: 1"},
	}, "")
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if !strings.HasPrefix(cmd, "timeout 300s schemathesis run ") {
		t.Errorf("命令应以 timeout 包装的 schemathesis 开头，实际: %s", cmd)
	}
	for _, want := range []string{"--base-url 'https://api.example.com'", "--max-examples 120", "--workers 1", "-H 'Authorization: Bearer abc'"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("命令缺少 %q: %s", want, cmd)
		}
	}
	if len(warnings) == 0 {
		t.Error("api 模式应给出参数版本差异提示")
	}
}

func TestFuzzCommandAPIRequiresContractAndURL(t *testing.T) {
	if _, _, err := fuzzCommand(fuzzPlanInput{Mode: "api", Target: "https://x"}, ""); err == nil {
		t.Fatal("缺 contract 应报错")
	}
	if _, _, err := fuzzCommand(fuzzPlanInput{Mode: "api", Contract: "openapi.json"}, ""); err == nil {
		t.Fatal("缺 target 应报错")
	}
	if _, _, err := fuzzCommand(fuzzPlanInput{Mode: "api", Contract: "o.json", Target: "ftp://x"}, ""); err == nil {
		t.Fatal("非 http(s) target 应报错")
	}
}

func TestFuzzCommandParam(t *testing.T) {
	if _, _, err := fuzzCommand(fuzzPlanInput{Mode: "param", Target: "https://x/a?id=1", Wordlist: "/wl.txt"}, ""); err == nil {
		t.Fatal("缺 FUZZ 占位应报错")
	}
	if _, _, err := fuzzCommand(fuzzPlanInput{Mode: "param", Target: "https://x/a?id=FUZZ"}, ""); err == nil {
		t.Fatal("缺 wordlist 应报错")
	}
	cmd, _, err := fuzzCommand(fuzzPlanInput{
		Mode: "param", Target: "https://x/a?id=FUZZ", Wordlist: "/wl/idor.txt", Rate: 999,
	}, "")
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if !strings.Contains(cmd, "-rate 50") {
		t.Errorf("速率应被夹到上限 50，实际: %s", cmd)
	}
	if !strings.Contains(cmd, "-w '/wl/idor.txt'") {
		t.Errorf("wordlist 应被单引号包裹，实际: %s", cmd)
	}
}

func TestFuzzCommandProtocol(t *testing.T) {
	if _, _, err := fuzzCommand(fuzzPlanInput{Mode: "protocol", Target: "10.0.0.5"}, ""); err == nil {
		t.Fatal("target 缺端口应报错")
	}
	if _, _, err := fuzzCommand(fuzzPlanInput{Mode: "protocol", Target: "10.0.0.5:6379"}, ""); err == nil {
		t.Fatal("没有脚本路径应报错（未启用）")
	}
	cmd, warnings, err := fuzzCommand(fuzzPlanInput{Mode: "protocol", Target: "10.0.0.5:6379", Duration: 60}, "/tmp/fuzz_protocol.py")
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if cmd != "timeout 60s python3 '/tmp/fuzz_protocol.py'" {
		t.Errorf("protocol 命令不符: %s", cmd)
	}
	if len(warnings) == 0 {
		t.Error("protocol 模式应提示需按真实报文改写")
	}
}

func TestFuzzCommandRejectsUnknownMode(t *testing.T) {
	if _, _, err := fuzzCommand(fuzzPlanInput{Mode: "whatever", Target: "x"}, ""); err == nil {
		t.Fatal("未知 mode 应报错")
	}
}

func TestValidateFuzzHeaders(t *testing.T) {
	// CRLF 注入。
	if _, err := validateFuzzHeaders([]string{"X: a\r\nInjected: b"}); err == nil {
		t.Fatal("CRLF 应被拒绝")
	}
	// @ 前缀（curl 会读本地文件）。
	if _, err := validateFuzzHeaders([]string{"@/etc/passwd"}); err == nil {
		t.Fatal("@ 前缀应被拒绝")
	}
	// 格式错误。
	if _, err := validateFuzzHeaders([]string{"NoColon"}); err == nil {
		t.Fatal("缺冒号应被拒绝")
	}
	// 归一化。
	got, err := validateFuzzHeaders([]string{"  X-A :  v  "})
	if err != nil {
		t.Fatalf("合法头不应报错: %v", err)
	}
	if len(got) != 1 || got[0] != "X-A: v" {
		t.Errorf("归一化结果不符: %v", got)
	}
}

func TestSplitHostPort(t *testing.T) {
	h, p, ok := splitHostPort("10.0.0.5:6379")
	if !ok || h != "10.0.0.5" || p != 6379 {
		t.Errorf("解析结果不符: %q %d %v", h, p, ok)
	}
	for _, bad := range []string{"", "10.0.0.5", ":80", "h:0", "h:70000", "h:abc"} {
		if _, _, ok := splitHostPort(bad); ok {
			t.Errorf("%q 应解析失败", bad)
		}
	}
}
