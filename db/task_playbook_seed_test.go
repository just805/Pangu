package db

import (
	"strings"
	"testing"
)

// 清掉任务模板种子的历史痕迹（共享测试库）。
func resetPlaybookSeeds(t *testing.T, d *DB) {
	t.Helper()
	_, _ = d.Exec(`DELETE FROM task_templates WHERE name LIKE 'Playbook：%'`)
	_, _ = d.Exec(`DELETE FROM settings WHERE key IN ('task_playbooks_v1','task_playbooks_v2')`)
}

// 内置 Playbook 目录本身的结构约束（不依赖数据库）。
func TestBuiltinPlaybooksCatalog(t *testing.T) {
	all := append(append([]builtinTaskPlaybook{}, builtinTaskPlaybooks...), builtinTaskPlaybooksV2...)
	if len(all) == 0 {
		t.Fatal("builtin playbooks is empty")
	}
	seen := map[string]bool{}
	for _, pb := range all {
		if !strings.HasPrefix(pb.name, "Playbook：") {
			t.Errorf("name %q 缺少 Playbook 前缀", pb.name)
		}
		if seen[pb.name] {
			t.Errorf("duplicate playbook name %q", pb.name)
		}
		seen[pb.name] = true
		if strings.TrimSpace(pb.description) == "" || strings.TrimSpace(pb.goal) == "" {
			t.Errorf("playbook %q 的 description/goal 不能为空", pb.name)
		}
		if !strings.Contains(pb.goal, "【内部工作流") && !strings.Contains(pb.goal, "【工作流") && !strings.Contains(pb.goal, "【产出】") {
			t.Errorf("playbook %q 缺少结构化工作流段", pb.name)
		}
	}
}

func TestSeedBuiltinTaskPlaybooks(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()
	resetPlaybookSeeds(t, d)

	if err := d.seedBuiltinTaskPlaybooks(); err != nil {
		t.Fatalf("seed v1: %v", err)
	}
	if err := d.seedBuiltinTaskPlaybooksV2(); err != nil {
		t.Fatalf("seed v2: %v", err)
	}
	list, err := d.ListTaskTemplates()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	foundV2 := false
	for _, tpl := range list {
		if !strings.HasPrefix(tpl.Name, "Playbook：") {
			continue
		}
		count++
		if tpl.Name == builtinTaskPlaybooksV2[0].name {
			foundV2 = true
		}
		if !strings.Contains(tpl.Goal, "【内部工作流") && !strings.Contains(tpl.Goal, "【工作流") && !strings.Contains(tpl.Goal, "【产出】") {
			t.Errorf("playbook %q 缺少结构化工作流段", tpl.Name)
		}
	}
	if want := len(builtinTaskPlaybooks) + len(builtinTaskPlaybooksV2); count != want {
		t.Fatalf("playbook 数=%d want %d", count, want)
	}
	if !foundV2 {
		t.Fatalf("v2 客户端逆向模板未补种：%q", builtinTaskPlaybooksV2[0].name)
	}
	// 幂等：再跑一次不新增、不报错（flag 已 done）。
	before := len(list)
	if err := d.seedBuiltinTaskPlaybooks(); err != nil {
		t.Fatalf("second seed v1: %v", err)
	}
	if err := d.seedBuiltinTaskPlaybooksV2(); err != nil {
		t.Fatalf("second seed v2: %v", err)
	}
	after, _ := d.ListTaskTemplates()
	if len(after) != before {
		t.Fatalf("重复种子不应新增：before=%d after=%d", before, len(after))
	}
}

// 存量实例（v1 已 done）升级时，v2 批次只补新模板，不覆盖用户改过的同名模板。
func TestSeedBuiltinTaskPlaybooksV2KeepsUserEdits(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()
	resetPlaybookSeeds(t, d)

	name := builtinTaskPlaybooksV2[0].name
	if _, err := d.CreateTaskTemplate(TaskTemplateInput{
		Name:        name,
		Description: "用户自定义描述",
		Goal:        "用户自定义目标",
	}); err != nil {
		t.Fatalf("pre-create template: %v", err)
	}
	if err := d.seedBuiltinTaskPlaybooksV2(); err != nil {
		t.Fatalf("seed v2: %v", err)
	}
	list, err := d.ListTaskTemplates()
	if err != nil {
		t.Fatal(err)
	}
	var got *TaskTemplate
	for i := range list {
		if list[i].Name == name {
			got = list[i]
			break
		}
	}
	if got == nil {
		t.Fatal("pre-created template disappeared")
	}
	if got.Goal != "用户自定义目标" || got.Description != "用户自定义描述" {
		t.Fatalf("播种覆盖了用户内容：%+v", got)
	}
	// 批次标记已置 done：再次启动不应重新插入。
	if v, _, _ := d.GetSetting("task_playbooks_v2"); v != "done" {
		t.Fatalf("task_playbooks_v2 flag=%q want done", v)
	}
}
