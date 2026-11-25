package db

import (
	"context"
	"testing"
)

func TestComplianceBaselinesBuiltin(t *testing.T) {
	names := ComplianceBaselines()
	if len(names) == 0 {
		t.Fatal("应至少有一个内置基线")
	}
	for _, n := range names {
		items, ok := ComplianceBaseline(n)
		if !ok || len(items) == 0 {
			t.Errorf("基线 %s 无检查项", n)
		}
		seen := map[string]bool{}
		for _, it := range items {
			if it.Key == "" || it.Title == "" || it.Method == "" || it.Expect == "" {
				t.Errorf("基线 %s 的项字段不全: %+v", n, it)
			}
			if seen[it.Key] {
				t.Errorf("基线 %s 的 key %s 重复", n, it.Key)
			}
			seen[it.Key] = true
		}
	}
	if _, ok := ComplianceBaseline("nope"); ok {
		t.Error("未知基线不应返回 ok")
	}
}

func TestComplianceResultUpsert(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	defer d.Close()
	ctx := context.Background()
	const taskID = int64(990101)
	store := d.Compliance()
	defer store.DeleteByTask(ctx, taskID)

	if err := store.UpsertResult(ctx, taskID, "cis-web", ComplianceResult{
		ItemKey: "tls-1", Status: "fail", Evidence: "支持 TLS1.0", Note: "需关停",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// 覆盖更新。
	if err := store.UpsertResult(ctx, taskID, "cis-web", ComplianceResult{
		ItemKey: "tls-1", Status: "pass", Evidence: "已禁用 TLS1.0",
	}); err != nil {
		t.Fatalf("upsert overwrite: %v", err)
	}
	rows, err := store.ListResults(ctx, taskID, "cis-web")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("同键应覆盖为 1 条，实际 %d", len(rows))
	}
	if rows[0].Status != "pass" || rows[0].Evidence != "已禁用 TLS1.0" {
		t.Fatalf("覆盖后内容不符: %+v", rows[0])
	}
	// 不同基线互不影响。
	if err := store.UpsertResult(ctx, taskID, "djbh2", ComplianceResult{ItemKey: "ident-1", Status: "pass"}); err != nil {
		t.Fatalf("upsert djbh2: %v", err)
	}
	rows2, _ := store.ListResults(ctx, taskID, "cis-web")
	if len(rows2) != 1 {
		t.Errorf("其它基线的写入不应影响 cis-web：%d", len(rows2))
	}
}
