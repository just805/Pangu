package db

import (
	"context"
	"testing"
)

func TestFindingFieldDefsSeedIdempotent(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	defer d.Close()
	ctx := context.Background()
	// Open 已 seed 过一次。
	defs, err := d.ListFindingFieldDefs(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(defs) != len(builtinFindingFields) {
		t.Fatalf("内置字段数应为 %d，实际 %d", len(builtinFindingFields), len(defs))
	}
	// 必填字段应包含 vulnclass/name/severity。
	req := map[string]bool{}
	for _, f := range defs {
		if f.Required {
			req[f.Key] = true
		}
	}
	for _, k := range []string{"vulnclass", "name", "severity"} {
		if !req[k] {
			t.Errorf("%s 应为必填", k)
		}
	}
	// cvss_vector 的 hint 要提到自动算分（模型据此知道该填向量）。
	for _, f := range defs {
		if f.Key == "cvss_vector" && f.Hint == "" {
			t.Error("cvss_vector 应有 hint")
		}
	}
	// 用户改过的 label 不被再次 seed 覆盖。
	if _, err := d.ExecContext(ctx, `UPDATE finding_field_defs SET label='自定义类别名' WHERE key='vulnclass'`); err != nil {
		t.Fatalf("update: %v", err)
	}
	defer d.ExecContext(ctx, `UPDATE finding_field_defs SET label='漏洞类别' WHERE key='vulnclass'`)
	if err := d.SeedFindingFieldDefs(ctx); err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	defs, _ = d.ListFindingFieldDefs(ctx)
	for _, f := range defs {
		if f.Key == "vulnclass" && f.Label != "自定义类别名" {
			t.Fatalf("再次 seed 不应覆盖用户改过的 label，实际 %q", f.Label)
		}
	}
}
