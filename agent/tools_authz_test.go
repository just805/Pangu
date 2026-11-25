package agent

import (
	"testing"

	"github.com/just805/pangu/db"
)

func TestNormalizeAuthzState(t *testing.T) {
	cases := map[string]string{
		"":        "unknown",
		"allow":   "allow",
		"ALLOWED": "allow",
		"允许":      "allow",
		"deny":    "deny",
		"拒绝":      "deny",
		"未知":      "unknown",
		"未测":      "unknown",
		"banana":  "",
	}
	for in, want := range cases {
		if got := normalizeAuthzState(in); got != want {
			t.Errorf("normalizeAuthzState(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeAuthzEntry(t *testing.T) {
	// role 缺失。
	if _, msg := normalizeAuthzEntry(db.AuthzEntry{Path: "/a"}); msg == "" {
		t.Fatal("缺 role 应报错")
	}
	// path 缺失。
	if _, msg := normalizeAuthzEntry(db.AuthzEntry{Role: "user"}); msg == "" {
		t.Fatal("缺 path 应报错")
	}
	// path 不以 / 开头。
	if _, msg := normalizeAuthzEntry(db.AuthzEntry{Role: "user", Path: "api/x"}); msg == "" {
		t.Fatal("path 必须以 / 开头")
	}
	// method 非法。
	if _, msg := normalizeAuthzEntry(db.AuthzEntry{Role: "user", Path: "/a", Method: "TRACE"}); msg == "" {
		t.Fatal("非法 method 应报错")
	}
	// 状态非法。
	if _, msg := normalizeAuthzEntry(db.AuthzEntry{Role: "user", Path: "/a", Expected: "banana"}); msg == "" {
		t.Fatal("非法 expected 应报错")
	}
	// 正常归一：小写方法、中文状态、空状态转 unknown。
	e, msg := normalizeAuthzEntry(db.AuthzEntry{
		Role: " user-a ", Path: " /api/orders/1 ", Method: "post",
		Expected: "拒绝", Observed: "", StatusCode: 403,
	})
	if msg != "" {
		t.Fatalf("应通过，实际: %s", msg)
	}
	if e.Role != "user-a" || e.Path != "/api/orders/1" || e.Method != "POST" {
		t.Errorf("归一化不符: %+v", e)
	}
	if e.Expected != "deny" || e.Observed != "unknown" {
		t.Errorf("状态归一不符: %+v", e)
	}
	// 状态码越界。
	if _, msg := normalizeAuthzEntry(db.AuthzEntry{Role: "u", Path: "/a", StatusCode: 999}); msg == "" {
		t.Fatal("状态码越界应报错")
	}
}
