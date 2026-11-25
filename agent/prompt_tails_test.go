package agent

import (
	"strings"
	"testing"
)

// 硬尾巴守门测试（v0.4.63）：扫描器优先与漏洞覆盖矩阵是代码注入
// 的提示词，不随 UI 改词消失；这里锁住关键锚点，防止后续重构把它们弄丢。
func TestScannerAndCoverageTails(t *testing.T) {
	w := workerSystem("", "", "/tmp", "/tmp/run", HiddenServiceOptions{})
	for _, want := range []string{"扫描器优先", "ffuf -u", "/opt/SecLists", "nuclei -u", "sqlmap -u", "技能路由"} {
		if !strings.Contains(w, want) {
			t.Errorf("worker system prompt missing %q", want)
		}
	}
	p := plannerSystem("目标", "/tmp", "/tmp/run", HiddenServiceOptions{})
	for _, want := range []string{"漏洞覆盖矩阵", "SQL 注入", "SSRF/回连", "文件上传", "弱口令", "ffuf/gobuster", "授权边界与复核优先"} {
		if !strings.Contains(p, want) {
			t.Errorf("planner system prompt missing %q", want)
		}
	}
}

// 隐藏服务提示段（Tor/I2P 收口）：配置了 Tor/I2P 时 worker/planner 提示词要
// 带流量特性与工具边界（nuclei 不走分流、Bash curl 要手动代理、串行探测）；
// 未配置时不注入（省 token）。
func TestHiddenServiceGuidance(t *testing.T) {
	off := HiddenServiceOptions{}
	if s := hiddenServiceGuidance(off); s != "" {
		t.Fatalf("未配置 Tor/I2P 不应注入: %s", s)
	}
	on := HiddenServiceOptions{Tor: "socks5h://127.0.0.1:9050", I2P: "http://127.0.0.1:4444"}
	w := workerSystem("", "", "/tmp", "/tmp/run", on)
	for _, want := range []string{
		"隐藏服务（Tor/I2P）流量特性",
		"socks5h://127.0.0.1:9050",
		"http://127.0.0.1:4444",
		"nuclei 与 WebFetch",
		"串行探测",
	} {
		if !strings.Contains(w, want) {
			t.Errorf("worker hidden guidance missing %q", want)
		}
	}
	p := plannerSystem("目标", "/tmp", "/tmp/run", on)
	for _, want := range []string{"隐藏服务派单约束", "不要派 nuclei", "串行探测"} {
		if !strings.Contains(p, want) {
			t.Errorf("planner hidden guidance missing %q", want)
		}
	}
	// 未配置时提示词不含隐藏服务段（IP 直连形态 zero-token）。
	if strings.Contains(workerSystem("", "", "/tmp", "/tmp/run", off), "隐藏服务（Tor/I2P）") {
		t.Fatal("未配置时 worker 提示词不应含隐藏服务段")
	}
}
