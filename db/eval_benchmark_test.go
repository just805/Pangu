package db

import (
	"context"
	"testing"
)

func TestScoreBenchmarkAllMatched(t *testing.T) {
	exp := []ExpectedFinding{
		{VulnClass: "sql_injection", AssetKeyword: "/api/login"},
		{VulnClass: "idor"},
	}
	act := []ActualFinding{
		{FindingID: 1, VulnClass: "sql_injection", AssetLabels: []string{"api.example.com", "/api/login"}},
		{FindingID: 2, VulnClass: "idor", AssetLabels: []string{"api.example.com"}},
	}
	s := ScoreBenchmark(exp, act)
	if s.Matched != 2 || len(s.Missed) != 0 || len(s.Unexpected) != 0 {
		t.Fatalf("应全部命中: %+v", s)
	}
	if s.Recall == nil || *s.Recall != 1 {
		t.Errorf("recall 应为 1: %+v", s.Recall)
	}
}

func TestScoreBenchmarkMissedAndUnexpected(t *testing.T) {
	exp := []ExpectedFinding{
		{VulnClass: "sql_injection", AssetKeyword: "/api/login"},
		{VulnClass: "ssrf"},
	}
	act := []ActualFinding{
		// 类别对但资产不匹配 → 不应算命中 sql_injection
		{FindingID: 1, VulnClass: "sql_injection", AssetLabels: []string{"/api/orders"}},
		// 预期之外的 xss
		{FindingID: 2, VulnClass: "xss", Name: "反射型 XSS"},
	}
	s := ScoreBenchmark(exp, act)
	if s.Matched != 0 {
		t.Errorf("不应有命中: %+v", s)
	}
	if len(s.Missed) != 2 {
		t.Errorf("应漏报 2 项，实际 %d", len(s.Missed))
	}
	if len(s.Unexpected) != 2 {
		t.Errorf("应 2 项预期外，实际 %d", len(s.Unexpected))
	}
	if s.Recall == nil || *s.Recall != 0 {
		t.Errorf("recall 应为 0")
	}
}

func TestScoreBenchmarkClassNormalization(t *testing.T) {
	// 标准答案写中文，实际发现写英文枚举——应视为同类。
	exp := []ExpectedFinding{{VulnClass: "SQL注入"}}
	act := []ActualFinding{{FindingID: 1, VulnClass: "sql_injection"}}
	s := ScoreBenchmark(exp, act)
	if s.Matched != 1 {
		t.Errorf("类别归一后应命中: %+v", s)
	}
}

func TestScoreBenchmarkNoDoubleCount(t *testing.T) {
	// 一条标准答案只能消耗一条实际发现：两条同类发现，第一条命中，第二条算 expected 外。
	exp := []ExpectedFinding{{VulnClass: "idor"}}
	act := []ActualFinding{
		{FindingID: 1, VulnClass: "idor"},
		{FindingID: 2, VulnClass: "idor"},
	}
	s := ScoreBenchmark(exp, act)
	if s.Matched != 1 || len(s.Unexpected) != 1 {
		t.Fatalf("不应重复计数: %+v", s)
	}
}

func TestEvalBenchmarkCRUD(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	defer d.Close()
	ctx := context.Background()
	store := d.EvalBenchmarks()
	name := "bench-test"
	_, _ = d.Exec(`DELETE FROM eval_benchmarks WHERE name=$1`, name)
	defer d.Exec(`DELETE FROM eval_benchmarks WHERE name=$1`, name)

	id, err := store.Create(ctx, name, "靶场", []ExpectedFinding{{VulnClass: "xss", AssetKeyword: "/search"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := store.Get(ctx, id)
	if err != nil || got == nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Expected) != 1 || got.Expected[0].AssetKeyword != "/search" {
		t.Fatalf("expected 读回不符: %+v", got.Expected)
	}
	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, b := range list {
		if b.ID == id {
			found = true
		}
	}
	if !found {
		t.Fatal("list 未返回刚创建的标准答案")
	}
	if err := store.Delete(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got, _ := store.Get(ctx, id); got != nil {
		t.Fatal("删除后不应还能取到")
	}
}
