package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// planner 预取态势的体积治理：全量超预算时按「先瘦身（裁重负载字段）、后裁骨
// （只留决策骨架）」两档降级，核心决策字段永远保留，且附带体积告警行让 planner
// 知道细节要按需查回而不是凭记忆脑补。
func TestRenderGraphOverviewWithinBudget(t *testing.T) {
	data := map[string]any{
		"task":          map[string]any{"goal": "x"},
		"frontier_open": 3,
	}
	out := renderGraphOverview(data)
	if !strings.Contains(out, "frontier_open") {
		t.Fatalf("小态势应全量保留, got %q", out)
	}
	if strings.Contains(out, "态势体积告警") {
		t.Fatalf("小态势不应出现裁剪告警, got %q", out)
	}
}

func TestRenderGraphOverviewTrimsHeavyFieldsWhenOversized(t *testing.T) {
	// 构造一个肯定超预算的态势：冷区 digest 正文 + 超长明细数组。
	big := strings.Repeat("冷区摘要正文。", 8000)
	data := map[string]any{
		"task":                map[string]any{"goal": "x"},
		"frontier_open":       3,
		"cold_digests":        []any{map[string]any{"id": 1, "body": big}},
		"recent_facts":        make([]any, 30),
		"finding_list":        make([]any, 30),
		"recent_done_intents": make([]any, 20),
		"stall":               map[string]any{"idle_rounds": 2},
	}
	out := renderGraphOverview(data)
	if strings.Contains(out, string(big[:20])) {
		t.Fatal("超预算时冷区 digest 正文应被整体裁掉（expand_digest 按需查回）")
	}
	// 决策骨架必须保留。
	for _, key := range []string{"frontier_open", "stall", "task"} {
		if !strings.Contains(out, key) {
			t.Fatalf("决策字段 %s 被误裁: %.200s", key, out)
		}
	}
	// 告警行必须出现，让 planner 知道细节要按需查。
	if !strings.Contains(out, "态势体积告警") {
		t.Fatalf("裁剪后应附体积告警行: %.200s", out)
	}
}

func TestRenderGraphOverviewSkeletonWhenStillOversized(t *testing.T) {
	// 重负载字段裁完仍超预算 → 第二档只留决策骨架（host 工具家底等被裁）。
	data := map[string]any{
		"task":          map[string]any{"goal": strings.Repeat("目标", 30000)},
		"frontier_open": 3,
		"tools":         map[string]any{"note": strings.Repeat("工具家底。", 20000)},
		"stall":         map[string]any{"idle_rounds": 2},
	}
	out := renderGraphOverview(data)
	if strings.Contains(out, "工具家底。") {
		t.Fatal("第二档裁剪应去掉非决策骨架字段")
	}
	if !strings.Contains(out, "frontier_open") || !strings.Contains(out, "stall") {
		t.Fatalf("决策骨架必须保留: %.300s", out)
	}
	start := strings.Index(out, "{")
	end := strings.LastIndex(out, "}")
	if start < 0 || end <= start {
		t.Fatalf("输出应含 JSON 态势: %.100s", out)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out[start:end+1]), &parsed); err != nil {
		t.Fatalf("裁剪后的 JSON 应仍然合法可解析: %v", err)
	}
}
