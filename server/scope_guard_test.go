package server

// N2 scope-aware 拦截的单测（纯函数层）：URL 提取、域名/子域/IP/CIDR 匹配。
// DB 依赖的 scopeForCompanies 由 CI 真 PG 全量覆盖。

import (
	"encoding/json"
	"net"
	"testing"
)

func TestHostsFromToolInput(t *testing.T) {
	in, _ := json.Marshal(map[string]any{
		"url":     "http://admin.target.example/login?next=/a",
		"headers": "Host: api.test.com\nX-Ref: https://other.org/x",
		"body":    "callback=https://callback.evil.cn/hook",
		"n":       123,
	})
	got := hostsFromToolInput(in)
	// map 遍历顺序不稳定——按集合比较。
	want := map[string]bool{"admin.target.example": true, "other.org": true, "callback.evil.cn": true}
	if len(got) != len(want) {
		t.Fatalf("hosts=%v want=%v", got, want)
	}
	for _, h := range got {
		if !want[h] {
			t.Fatalf("unexpected host %q in %v", h, got)
		}
	}
	// 非 JSON 文本（Bash 命令）也扫得到。
	if got := hostsFromToolInput([]byte("curl https://x.example.com/api -H 'Referer: http://y.org/'")); len(got) != 2 {
		t.Fatalf("bash text hosts=%v", got)
	}
}

func TestHostInScope(t *testing.T) {
	_, cidr, _ := net.ParseCIDR("10.20.0.0/16")
	domains := []string{"target.example", "allowed.cn"}
	nets := []*net.IPNet{cidr}
	cases := []struct {
		host string
		want bool
	}{
		{"target.example", true},
		{"admin.target.example", true}, // 子域
		{"TARGET.EXAMPLE.", true},      // 大小写 + 尾点
		{"notarget.example", false},    // 后缀伪装
		{"evil.org", false},
		{"10.20.3.4", true},
		{"10.21.3.4", false},
	}
	for _, c := range cases {
		if got := hostInScope(c.host, domains, nets); got != c.want {
			t.Errorf("hostInScope(%q)=%v want %v", c.host, got, c.want)
		}
	}
}
