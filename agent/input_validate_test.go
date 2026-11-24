package agent

import (
	"strings"
	"testing"
)

func TestValidateCurlHeaderValue(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"正常认证头", "Authorization: Bearer abc.def", false},
		{"空值", "", false},
		{"含空格与分号", "X-Test: a; b=c", false},
		{"CR 注入", "X-Test: 1\rX-Evil: 2", true},
		{"LF 注入", "X-Test: 1\nX-Evil: 2", true},
		{"CRLF 注入", "X-Test: 1\r\nX-Evil: 2", true},
		{"TAB", "X-Test:\t1", true},
		{"NUL", "X-Test: 1\x00", true},
		{"@file", "@/etc/passwd", true},
		{"中文", "X-Test: 中文", true},
		{"超长", "X-Test: " + strings.Repeat("a", maxHeaderValueBytes), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateCurlHeaderValue(c.in)
			if (err != nil) != c.wantErr {
				t.Fatalf("validateCurlHeaderValue(%q) err=%v, wantErr=%v", c.in, err, c.wantErr)
			}
		})
	}
}

func TestNormalizeToolUserAgent(t *testing.T) {
	if got, err := normalizeToolUserAgent("   "); err != nil || got != "" {
		t.Fatalf("空 UA 应归一为空串: %q %v", got, err)
	}
	got, err := normalizeToolUserAgent("  CustomUA/1.0 (X11)  ")
	if err != nil || got != "CustomUA/1.0 (X11)" {
		t.Fatalf("应 trim 并通过: %q %v", got, err)
	}
	if _, err := normalizeToolUserAgent("a\r\nb"); err == nil {
		t.Fatal("CRLF UA 应被拒绝")
	}
	if _, err := normalizeToolUserAgent("@" + strings.Repeat("a", 10)); err == nil {
		t.Fatal("@ 前缀 UA 应被拒绝")
	}
	if _, err := normalizeToolUserAgent(strings.Repeat("a", maxUserAgentBytes+1)); err == nil {
		t.Fatal("超长 UA 应被拒绝")
	}
}

// 恶意头必须让命令构建直接失败，而不是把换行/文件引用拼进 curl。
func TestAuthCurlRejectsHeaderInjection(t *testing.T) {
	e := authEntry{Token: "tk"}
	if _, err := buildAuthCurl("https://example.com", "GET", "", "X-Test: 1\r\nX-Evil: 2", e); err == nil {
		t.Fatal("auth_request 的 CRLF 头应被拒绝")
	}
	if _, err := buildAuthCurl("https://example.com", "GET", "", "@/etc/passwd", e); err == nil {
		t.Fatal("auth_request 的 @file 头应被拒绝")
	}
	ok, err := buildAuthCurl("https://example.com", "GET", "", "X-Test: 1", e)
	if err != nil || !strings.Contains(ok, "-H 'X-Test: 1'") {
		t.Fatalf("合法头应正常组装: %q %v", ok, err)
	}
	// 多行头是显式设计：\n 分隔应当允许。
	if _, err := buildAuthCurl("https://example.com", "GET", "", "X-A: 1\nX-B: 2", e); err != nil {
		t.Fatalf("\\n 分隔的多行头应允许: %v", err)
	}
}

func TestDiffScriptRejectsHeaderInjection(t *testing.T) {
	a := diffArgs{URLA: "https://a.example.com", URLB: "https://b.example.com", HeaderA: "Authorization: Bearer a\r\nX-Evil: 2"}
	if _, err := buildDiffScript(a, "/tmp/x.py", "/tmp/w"); err == nil {
		t.Fatal("diff_responses 的 CRLF 头应被拒绝")
	}
	a.HeaderA = "Authorization: Bearer a"
	if _, err := buildDiffScript(a, "/tmp/x.py", "/tmp/w"); err != nil {
		t.Fatalf("合法头应正常组装: %v", err)
	}
}
