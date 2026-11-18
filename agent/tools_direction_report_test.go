package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/just805/pangu/db"
)

func reportJSON(t *testing.T, rep map[string]any) map[string]any {
	t.Helper()
	b, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// direction_report（方向产出率报表）：按 token 降序、零产出计数、汇总字段。
func TestDirectionReport(t *testing.T) {
	node := func(id int64, summary, state, stop string) *db.Node {
		return &db.Node{ID: id, Kind: db.KindIntent, State: state, BlockedReason: stop,
			Payload: []byte(`{"summary":"` + summary + `"}`)}
	}
	done := []*db.Node{
		node(3, "sql 注入 dump", "exhausted", "手段穷尽"),
		node(2, "目录爆破", "done", ""),
		node(1, "端口扫描", "blocked", "WAF"),
	}
	yieldsOf := map[int64][]int64{2: {10, 11, 12}, 1: {}}
	tokens := map[int64]int64{1: 90000, 2: 5000, 3: 40000}

	rep := reportJSON(t, directionReport(done, yieldsOf, tokens))
	if rep["zero_yield"].(float64) != 2 {
		// id=1（blocked）与 id=3（exhausted）都无 yields；id=2 产出 3 条。
		t.Fatalf("零产出计数应为 2, got %v", rep["zero_yield"])
	}
	if rep["total_tokens"].(float64) != 135000 {
		t.Fatalf("total_tokens 应为 135000, got %v", rep["total_tokens"])
	}
	if rep["done_total"].(float64) != 3 {
		t.Fatalf("done_total 应为 3, got %v", rep["done_total"])
	}
	if !strings.Contains(rep["hint"].(string), "yields=0") {
		t.Fatal("hint 应说明零产出方向的处置语义")
	}
	rows := rep["intents"].([]any)
	first := rows[0].(map[string]any)
	if first["id"].(float64) != 1 {
		t.Fatalf("token 最高（id=1, 90000）应排第一, got %v", first["id"])
	}
	if first["stop_reason"] != "WAF" {
		t.Fatalf("stop_reason 应随行带出, got %v", first["stop_reason"])
	}
}

// top 截断：9 条只保留 token 最高的 8 条。
func TestDirectionReportCapsAtEight(t *testing.T) {
	node := func(id int64) *db.Node {
		return &db.Node{ID: id, Kind: db.KindIntent, State: "done", Payload: []byte(`{"summary":"s"}`)}
	}
	done := make([]*db.Node, 0, 9)
	tokens := map[int64]int64{}
	for i := int64(1); i <= 9; i++ {
		done = append(done, node(i))
		tokens[i] = i * 100
	}
	rep := reportJSON(t, directionReport(done, map[int64][]int64{}, tokens))
	rows := rep["intents"].([]any)
	if len(rows) != 8 {
		t.Fatalf("top 截断应为 8, got %d", len(rows))
	}
	first := rows[0].(map[string]any)
	if first["id"].(float64) != 9 {
		t.Fatalf("token 最高的意图应排第一, got %v", first["id"])
	}
	if first["yields"].(float64) != 0 {
		t.Fatalf("yields 应为 0, got %v", first["yields"])
	}
}
