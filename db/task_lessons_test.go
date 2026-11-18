package db

import (
	"context"
	"strings"
	"testing"
)

// containsLesson 断言结果里含有指定文本的行（共享测试库里可能有其它包的
// 异步收官写入——server 包测试触发 collectTaskLessons 会插通用行——所以
// 本测试只用「包含」断言而非精确计数，开头全表清空 + 结尾全表清理把污染
// 窗口压到最小）。
func containsLesson(rows []TaskLesson, text string) bool {
	for _, r := range rows {
		if strings.Contains(r.Text, text) {
			return true
		}
	}
	return false
}

// 跨任务经验沉淀（P1）：幂等重写、企业交集可见性、排除自身任务。
func TestTaskLessonsReplaceAndQuery(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()
	ctx := context.Background()
	// 共享库全表清理：server 包的收官测试会异步往这张表插通用经验行，
	// 只删自己的 task_id 挡不住跨包污染（-p 1 顺序跑 server 先于 db）。
	d.Exec(`DELETE FROM task_lessons`)
	t.Cleanup(func() { d.Exec(`DELETE FROM task_lessons WHERE task_id IN (888801, 888802)`) })

	// 任务 888801 归属企业 [1,2]：两条手法 + 一条教训。
	err = d.ReplaceTaskLessons(ctx, 888801, []int64{1, 2}, []TaskLesson{
		{Kind: "technique", Text: "有效手法（已完成方向）：默认口令进后台", SourceIntentID: 100},
		{Kind: "technique", Text: "有效手法（已完成方向）：备份文件路径枚举", SourceIntentID: 101},
		{Kind: "lesson", Text: "受限教训（被拦方向）：SQL 注入（原因：WAF 拦截 union）", SourceIntentID: 102},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 任务 888802 无企业归属：一条通用经验（全站可见）。
	if err := d.ReplaceTaskLessons(ctx, 888802, nil, []TaskLesson{
		{Kind: "technique", Text: "有效手法（已完成方向）：Swagger 文档直读接口"},
	}); err != nil {
		t.Fatal(err)
	}

	// 企业 2 的视角，排除自身 888802：888801 的 3 条必须齐全（888802 的通用行
	// 属于自身，被排除——不给本任务自己的经验回声）。
	visible, err := d.TaskLessonsForCompanies(ctx, []int64{2}, 888802, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"默认口令进后台", "备份文件路径枚举", "SQL 注入"} {
		if !containsLesson(visible, want) {
			t.Fatalf("企业 2 排除自身后应看到 888801 的三条, 缺 %q（got %d 条）", want, len(visible))
		}
	}
	for _, r := range visible {
		if strings.Contains(r.Text, "Swagger") {
			t.Fatalf("排除自身后不应看到 888802 的通用行: %+v", visible)
		}
	}

	// 企业 2 不排除：888801 交集 3 + 自身通用 1，全部可见。
	visibleAll, err := d.TaskLessonsForCompanies(ctx, []int64{2}, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"默认口令进后台", "备份文件路径枚举", "SQL 注入", "Swagger"} {
		if !containsLesson(visibleAll, want) {
			t.Fatalf("企业 2 不排除应看到全部 4 条, 缺 %q（got %d 条）", want, len(visibleAll))
		}
	}

	// 企业 9 无交集：只能看到通用经验（888802 那条空归属行）。
	other, err := d.TaskLessonsForCompanies(ctx, []int64{9}, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !containsLesson(other, "Swagger") {
		t.Fatalf("无交集企业应看到通用经验, got %d 条", len(other))
	}
	for _, r := range other {
		if strings.Contains(r.Text, "默认口令") || strings.Contains(r.Text, "SQL 注入") {
			t.Fatalf("无交集企业不应看到带企业归属的行: %+v", other)
		}
	}

	// 无归属任务（内部任务）：可见全部。
	all, err := d.TaskLessonsForCompanies(ctx, nil, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"默认口令进后台", "Swagger"} {
		if !containsLesson(all, want) {
			t.Fatalf("无归属任务应看到全部, 缺 %q（got %d 条）", want, len(all))
		}
	}

	// 幂等重写：同任务再收官只保留最新一份。
	if err := d.ReplaceTaskLessons(ctx, 888801, []int64{1, 2}, []TaskLesson{
		{Kind: "technique", Text: "重写后的唯一一条"},
	}); err != nil {
		t.Fatal(err)
	}
	visible, err = d.TaskLessonsForCompanies(ctx, []int64{2}, 888802, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !containsLesson(visible, "重写后的唯一一条") {
		t.Fatalf("幂等重写失败: %d 条", len(visible))
	}
	for _, r := range visible {
		if strings.Contains(r.Text, "默认口令") {
			t.Fatalf("重写后旧 888801 行应被清掉: %+v", visible)
		}
	}
}
