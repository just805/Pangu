package agent

import (
	"strings"
	"testing"
)

// bigSituation 造一份体积超过阈值（24KB）的态势：40 条长正文事实。
func bigSituation(extra ...int) map[string]any {
	facts := make([]map[string]any, 0, 40+len(extra))
	for i := 1; i <= 40; i++ {
		facts = append(facts, map[string]any{
			"id":      float64(i),
			"summary": strings.Repeat("很长的观察正文", 100),
		})
	}
	for _, id := range extra {
		facts = append(facts, map[string]any{
			"id":      float64(id),
			"summary": strings.Repeat("新增方向正文", 100),
		})
	}
	return map[string]any{
		"recent_facts": facts,
		"goals":        []map[string]any{{"id": float64(1), "state": "open", "text": "g"}},
	}
}

// TestSituationIncrementalOffByDefault：开关关闭时必须和全量渲染完全一致——
// 默认行为零变化是这个特性的前提。
func TestSituationIncrementalOffByDefault(t *testing.T) {
	p := &Planner{}
	data := bigSituation()
	if got, want := p.renderSituation(7, data), renderGraphOverview(data); got != want {
		t.Error("开关未开启时输出应与 renderGraphOverview 完全一致")
	}
}

// TestSituationIncrementalSkipsSmallData：体积没到阈值不做增量（小任务省不了多少）。
func TestSituationIncrementalSkipsSmallData(t *testing.T) {
	p := &Planner{}
	p.SetIncrementalSituation(func() bool { return true })
	small := map[string]any{"goals": []map[string]any{{"id": float64(1), "text": "g"}}}
	if got, want := p.renderSituation(7, small), renderGraphOverview(small); got != want {
		t.Error("小态势应走全量")
	}
}

// TestSituationIncrementalSecondRound：第二轮压缩已见条目，新条目仍全量，
// 且整体体积必须变小（压不动就退回全量，不能负优化）。
func TestSituationIncrementalSecondRound(t *testing.T) {
	p := &Planner{}
	p.SetIncrementalSituation(func() bool { return true })

	first := bigSituation()
	out1 := p.renderSituation(7, first)
	if strings.Contains(out1, "增量模式") {
		t.Error("首轮没有历史可压缩，不应进入增量模式")
	}

	second := bigSituation(41) // 新增一条
	out2 := p.renderSituation(7, second)
	if !strings.Contains(out2, "增量模式") {
		t.Fatal("第二轮应进入增量模式")
	}
	if !strings.Contains(out2, "（已见）") {
		t.Error("已见条目正文应被标注压缩")
	}
	if !strings.Contains(out2, strings.Repeat("新增方向正文", 100)) {
		t.Error("新条目正文应保持全量")
	}
	if len(out2) >= len(out1) {
		t.Errorf("增量输出不应大于首轮全量：%d vs %d", len(out2), len(out1))
	}
}

// TestSituationIncrementalPerTask：状态按探索隔离，A 任务的注入历史不能让 B 任务
// 的条目被误判成「已见」。
func TestSituationIncrementalPerTask(t *testing.T) {
	p := &Planner{}
	p.SetIncrementalSituation(func() bool { return true })
	p.renderSituation(7, bigSituation())
	out := p.renderSituation(8, bigSituation()) // 另一个探索，第一次
	if strings.Contains(out, "增量模式") {
		t.Error("不同探索之间不应共享注入历史")
	}
}

func TestSituationID(t *testing.T) {
	cases := []struct {
		in   map[string]any
		want int64
	}{
		{map[string]any{"id": float64(12)}, 12},
		{map[string]any{"id": int64(13)}, 13},
		{map[string]any{"id": 14}, 14},
		{map[string]any{"id": "15"}, 0}, // 字符串 id 不认，宁可全量
		{map[string]any{}, 0},
	}
	for _, c := range cases {
		if got := situationID(c.in); got != c.want {
			t.Errorf("situationID(%v)=%d, want %d", c.in, got, c.want)
		}
	}
}

func TestCompactSeenItemKeepsStructure(t *testing.T) {
	long := strings.Repeat("字", plannerSeenTextCap+50)
	in := map[string]any{"id": float64(3), "state": "confirmed", "summary": long, "short": "ok"}
	out := compactSeenItem(in)
	if out["id"] != float64(3) || out["state"] != "confirmed" {
		t.Error("结构与 id 必须保留")
	}
	if out["short"] != "ok" {
		t.Error("短文本不应被改动")
	}
	s, _ := out["summary"].(string)
	if !strings.HasSuffix(s, "…（已见）") {
		t.Errorf("长文本应被截断标注，实际尾部 %q", s[len(s)-20:])
	}
	if len([]rune(s)) > plannerSeenTextCap+10 {
		t.Errorf("截断后长度异常：%d", len([]rune(s)))
	}
	// 原 map 不能被就地改掉（maps.Clone 的意义）。
	if len([]rune(in["summary"].(string))) != plannerSeenTextCap+50 {
		t.Error("不应修改入参")
	}
}
