package agent

import (
	"strings"
	"testing"
)

// TestBuildDiffScriptWithInterpreter 锁住「解释器名由探测结果决定」：
// Windows 上只装了 python.exe，硬编码 python3 会让整条命令 exit 9009。
func TestBuildDiffScriptWithInterpreter(t *testing.T) {
	a := diffArgs{URLA: "http://a.example/x", URLB: "http://b.example/x"}
	cmd, err := buildDiffScriptWith("python", a, "/work/diff.py", "/work")
	if err != nil {
		t.Fatalf("组命令失败: %v", err)
	}
	if !strings.Contains(cmd, "python '/work/diff.py'") {
		t.Errorf("命令应使用探测到的解释器，实际：%s", cmd)
	}
	if strings.Contains(cmd, "python3 ") {
		t.Errorf("不应再硬编码 python3：%s", cmd)
	}
	// 两次取响应与状态码参数都必须在。
	for _, frag := range []string{"curl", "resp_a_body", "resp_b_body", `"$S"`, `"$T"`} {
		if !strings.Contains(cmd, frag) {
			t.Errorf("命令缺少 %q：%s", frag, cmd)
		}
	}
}

// TestBuildDiffScriptWithoutInterpreter：缺解释器必须返回哨兵错误，
// 让工具层换成带替代路径的提示，而不是生成一条注定失败的命令。
func TestBuildDiffScriptWithoutInterpreter(t *testing.T) {
	a := diffArgs{URLA: "http://a.example/x", URLB: "http://b.example/x"}
	if _, err := buildDiffScriptWith("", a, "/work/diff.py", "/work"); err != errNoPython {
		t.Errorf("缺解释器应返回 errNoPython，实际 %v", err)
	}
}

// TestBuildContractCmdWithInterpreter：契约解析同理，解释器名由调用方注入。
func TestBuildContractCmdWithInterpreter(t *testing.T) {
	cmd := buildContractCmd("python", "https://t.example.com", []string{"/openapi.json", "/swagger.json"},
		"/work/api_contract.py", "/work")
	if !strings.Contains(cmd, "python '/work/api_contract.py'") {
		t.Errorf("命令应使用探测到的解释器，实际：%s", cmd)
	}
	if !strings.Contains(cmd, "for u in ") || !strings.Contains(cmd, "CONTRACT $u") {
		t.Errorf("探测循环缺失：%s", cmd)
	}
}

// TestPythonMissingMessageIsActionable：提示必须给出「现在就能做」的替代路径，
// 只说「没有 python」等于把模型扔在原地。
func TestPythonMissingMessageIsActionable(t *testing.T) {
	msg := pythonMissingMessage("改用文件模式传 file_a/file_b。")
	for _, frag := range []string{"Python 3", "改用文件模式", "PATH"} {
		if !strings.Contains(msg, frag) {
			t.Errorf("提示缺少 %q：%s", frag, msg)
		}
	}
}

// TestPythonInterpreterCachedDeterministic：探测结果在一次进程内必须稳定
// （sync.OnceValue），否则同一 run 里两条命令可能用不同解释器。
func TestPythonInterpreterCachedDeterministic(t *testing.T) {
	first := pythonInterpreter()
	t.Logf("本机探测到的 Python 3 命令：%q（空=没有可用解释器）", first)
	for i := 0; i < 3; i++ {
		if got := pythonInterpreter(); got != first {
			t.Fatalf("探测结果不稳定：%q vs %q", first, got)
		}
	}
	// 本机有没有 python 都可能，但若探测到了，必须是 python3 或 python。
	switch first {
	case "", "python3", "python":
	default:
		t.Errorf("探测结果异常：%q", first)
	}
}
