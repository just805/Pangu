package agent

import (
	"strings"
	"testing"

	"github.com/just805/pangu/db"
)

func TestBuildToolPerfHintsFiltersAndOrders(t *testing.T) {
	stats := []db.ToolLatencyStat{
		{Role: "worker", Tool: "bash", Calls: 4, Errors: 4},              // 样本太稀疏，丢弃
		{Role: "planner", Tool: "graph_overview", Calls: 20, Errors: 20}, // 非 worker，丢弃
		{Role: "worker", Tool: "curl", Calls: 10, Errors: 9},             // 90%
		{Role: "worker", Tool: "nmap", Calls: 8, Errors: 8},              // 100%
		{Role: "worker", Tool: "sqlmap", Calls: 10, Errors: 2},           // 20%，丢弃
		{Role: "worker", Tool: "http_request", Calls: 10, Errors: 6},     // 60%，边界保留
	}
	hints := BuildToolPerfHints(stats)
	if len(hints) != 3 {
		t.Fatalf("want 3 hints, got %d: %+v", len(hints), hints)
	}
	if hints[0].Tool != "nmap" || hints[1].Tool != "curl" || hints[2].Tool != "http_request" {
		t.Fatalf("hints not ordered by error rate desc: %+v", hints)
	}
}

func TestBuildToolPerfHintsSparseReturnsNil(t *testing.T) {
	stats := []db.ToolLatencyStat{
		{Role: "worker", Tool: "bash", Calls: 3, Errors: 3},
	}
	if hints := BuildToolPerfHints(stats); hints != nil {
		t.Fatalf("sparse samples must not produce hints, got %+v", hints)
	}
}

func TestToolPerfBlock(t *testing.T) {
	if got := ToolPerfBlock(nil); got != "" {
		t.Fatalf("nil hints must render empty, got %q", got)
	}
	block := ToolPerfBlock([]ToolPerfHint{{Tool: "nmap", Calls: 8, Errors: 8, ErrorRate: 1.0}})
	if !strings.Contains(block, "nmap") || !strings.Contains(block, "100%") {
		t.Fatalf("block missing tool or rate: %q", block)
	}
}
