package agent

import (
	"testing"

	actool "github.com/Autumn-27/norma/tool"
)

func TestLimitToolVisibilityDefersOverflow(t *testing.T) {
	t.Setenv("PANGU_TOOL_VISIBILITY_LIMIT", "3")
	var tools []actool.CoreTool
	// 3 个非核心 + 1 个核心，上限 3：核心必留，另外留 2 个，其余 deferred。
	tools = append(tools,
		&stubTool{name: "tool_a"}, &stubTool{name: "tool_b"},
		&stubTool{name: "record_fact"}, &stubTool{name: "tool_c"}, &stubTool{name: "tool_d"},
	)
	def := limitToolVisibility(tools, DeferredInfo{})
	if len(def.Deferred) != 2 {
		t.Fatalf("应 defer 2 个，得到 %v", def.Deferred)
	}
	for _, n := range []string{"record_fact", "tool_a", "tool_b"} {
		for _, d := range def.Deferred {
			if d == n {
				t.Fatalf("核心/先到工具 %s 不应被 defer", n)
			}
		}
	}
	if len(def.GlobalNames) != len(def.Deferred) {
		t.Fatal("GlobalNames 应与 Deferred 同步")
	}
}

func TestLimitToolVisibilityDisabled(t *testing.T) {
	t.Setenv("PANGU_TOOL_VISIBILITY_LIMIT", "0")
	var tools []actool.CoreTool
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		tools = append(tools, &stubTool{name: n})
	}
	if def := limitToolVisibility(tools, DeferredInfo{}); len(def.Deferred) != 0 {
		t.Fatalf("关闭时不应 defer: %v", def.Deferred)
	}
}

func TestLimitToolVisibilityNoDuplicate(t *testing.T) {
	t.Setenv("PANGU_TOOL_VISIBILITY_LIMIT", "1")
	tools := []actool.CoreTool{&stubTool{name: "record_fact"}, &stubTool{name: "x"}, &stubTool{name: "y"}}
	def := limitToolVisibility(tools, DeferredInfo{Deferred: []string{"x"}, GlobalNames: []string{"x"}})
	seen := map[string]int{}
	for _, n := range def.Deferred {
		seen[n]++
	}
	for n, c := range seen {
		if c > 1 {
			t.Fatalf("deferred 重复: %s x%d", n, c)
		}
	}
}
