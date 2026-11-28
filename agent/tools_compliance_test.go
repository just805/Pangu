package agent

import "testing"

func TestNormalizeComplianceStatus(t *testing.T) {
	cases := map[string]string{
		"pass":    "pass",
		"符合":      "pass",
		"通过":      "pass",
		"fail":    "fail",
		"不符合":     "fail",
		"不通过":     "fail",
		"na":      "na",
		"N/A":     "na",
		"不适用":     "na",
		"":        "unknown",
		"unknown": "unknown",
		"未测":      "unknown",
	}
	for in, want := range cases {
		got, ok := normalizeComplianceStatus(in)
		if !ok || got != want {
			t.Errorf("normalizeComplianceStatus(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
	if _, ok := normalizeComplianceStatus("banana"); ok {
		t.Error("非法状态应返回 ok=false")
	}
}
