package db

import (
	"context"
	"testing"
)

// tool_latency 表的落笔与聚合（PG 集成）：insert 三行、按 (role,tool) 聚合出
// count/avg/p95/max 与错误率；task 过滤生效；延迟纯旁路（insert 失败不冒泡）。
func TestToolLatencyInsertAndAggregate(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()
	ctx := context.Background()
	d.Exec(`DELETE FROM tool_latency WHERE tool IN ('t-nmap','t-curl')`)

	d.InsertToolLatency(ctx, ToolLatencyRow{TaskID: 7777, Role: "worker", Tool: "t-nmap", LatencyMS: 1000})
	d.InsertToolLatency(ctx, ToolLatencyRow{TaskID: 7777, Role: "worker", Tool: "t-nmap", LatencyMS: 3000, IsError: true})
	d.InsertToolLatency(ctx, ToolLatencyRow{TaskID: 7778, Role: "planner", Tool: "t-curl", LatencyMS: 50})

	stats, err := d.ToolLatencyAggregate(ctx, 7777, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 {
		t.Fatalf("task 过滤后应只剩 1 组, got %d", len(stats))
	}
	s := stats[0]
	if s.Tool != "t-nmap" || s.Role != "worker" {
		t.Fatalf("聚合键不对: %+v", s)
	}
	if s.Calls != 2 {
		t.Fatalf("calls 应为 2, got %d", s.Calls)
	}
	if s.AvgMS != 2000 {
		t.Fatalf("avg 应为 2000, got %v", s.AvgMS)
	}
	if s.MaxMS != 3000 {
		t.Fatalf("max 应为 3000, got %d", s.MaxMS)
	}
	if s.Errors != 1 {
		t.Fatalf("errors 应为 1, got %d", s.Errors)
	}
	// percentile_cont 在 [1000,3000] 两行上连续插值：1000 + 0.95×2000 = 2900。
	if s.P95MS != 2900 {
		t.Fatalf("两行样本的 p95 应为 2900, got %v", s.P95MS)
	}
	// 全站聚合能看到另一个 task 的行。
	all, err := d.ToolLatencyAggregate(ctx, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("无过滤应聚出 2 组, got %d", len(all))
	}
	d.Exec(`DELETE FROM tool_latency WHERE tool IN ('t-nmap','t-curl')`)
}
