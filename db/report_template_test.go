package db

import (
	"context"
	"errors"
	"testing"
)

func TestReportTemplateCRUD(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	defer d.Close()
	ctx := context.Background()
	store := d.ReportTemplates()
	name := "tpl-test"
	_, _ = d.Exec(`DELETE FROM report_templates WHERE name LIKE $1`, name+"%")
	defer d.Exec(`DELETE FROM report_templates WHERE name LIKE $1`, name+"%")

	id1, err := store.Create(ctx, ReportTemplate{
		Name: name + "-a", PageHeader: "页眉 A", Classification: "机密",
		CustomerOverride: "某客户", Sections: map[string]bool{"attack_chain": false},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id2, err := store.Create(ctx, ReportTemplate{Name: name + "-b"})
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	// 设为默认：默认唯一。
	if err := store.SetDefault(ctx, id1); err != nil {
		t.Fatalf("set default: %v", err)
	}
	def, err := store.Default(ctx)
	if err != nil || def == nil {
		t.Fatalf("default: %v", err)
	}
	if def.ID != id1 || def.Sections["attack_chain"] != false {
		t.Fatalf("默认模板不符: %+v", def)
	}
	// 切换默认，旧的自动取消。
	if err := store.SetDefault(ctx, id2); err != nil {
		t.Fatalf("set default 2: %v", err)
	}
	def, _ = store.Default(ctx)
	if def.ID != id2 {
		t.Fatalf("默认应切到 id2，实际 %d", def.ID)
	}
	list, _ := store.List(ctx)
	defaults := 0
	for _, tp := range list {
		if tp.IsDefault {
			defaults++
		}
	}
	if defaults != 1 {
		t.Errorf("默认模板应唯一，实际 %d 个", defaults)
	}
	// 默认模板不允许删除。
	if err := store.Delete(ctx, id2); !errors.Is(err, ErrDefaultTemplateDelete) {
		t.Fatalf("删除默认模板应返回 ErrDefaultTemplateDelete，实际 %v", err)
	}
	// 非默认可删。
	if err := store.Delete(ctx, id1); err != nil {
		t.Fatalf("delete non-default: %v", err)
	}
}

func TestReportTemplateUpdateSections(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	defer d.Close()
	ctx := context.Background()
	store := d.ReportTemplates()
	name := "tpl-upd"
	_, _ = d.Exec(`DELETE FROM report_templates WHERE name=$1`, name)
	defer d.Exec(`DELETE FROM report_templates WHERE name=$1`, name)

	id, err := store.Create(ctx, ReportTemplate{Name: name})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.Update(ctx, ReportTemplate{
		ID: id, Name: name, PageHeader: "新页眉",
		Sections: map[string]bool{"coverage": false, "reverse": false},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	list, _ := store.List(ctx)
	for _, tp := range list {
		if tp.ID != id {
			continue
		}
		if tp.PageHeader != "新页眉" {
			t.Errorf("页眉未更新: %q", tp.PageHeader)
		}
		if tp.Sections["coverage"] || tp.Sections["reverse"] {
			t.Errorf("章节开关未写入: %+v", tp.Sections)
		}
	}
}
