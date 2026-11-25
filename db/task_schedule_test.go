package db

import (
	"context"
	"testing"
	"time"
)

func TestTaskScheduleCRUD(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	defer d.Close()
	ctx := context.Background()
	store := d.TaskSchedules()

	name := "schedule-crud-test"
	_, _ = d.Exec(`DELETE FROM task_schedules WHERE name=$1`, name)
	defer d.Exec(`DELETE FROM task_schedules WHERE name=$1`, name)

	// 周期下限校验。
	if _, err := store.Create(ctx, TaskSchedule{Name: name, Goal: "g", IntervalMinutes: 5}); err == nil {
		t.Fatal("小于最小周期应报错")
	}

	id, err := store.Create(ctx, TaskSchedule{
		Name: name, Description: "d", Goal: "扫 example.com",
		IntervalMinutes: 60, Enabled: true, CompanyIDs: []int64{1, 2},
		TimeoutSeconds: 3600, CoverageEnabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := store.Get(ctx, id)
	if err != nil || got == nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != name || got.IntervalMinutes != 60 || len(got.CompanyIDs) != 2 {
		t.Fatalf("读回不符: %+v", got)
	}

	// 新建即到点（next_run_at=now），Due 应该能取到。
	due, err := store.Due(ctx, 10)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	found := false
	for _, sc := range due {
		if sc.ID == id {
			found = true
		}
	}
	if !found {
		t.Fatal("新建的计划应立刻到期")
	}

	// 更新参数。
	got.IntervalMinutes = 120
	got.Enabled = false
	if err := store.Update(ctx, *got); err != nil {
		t.Fatalf("update: %v", err)
	}
	got2, _ := store.Get(ctx, id)
	if got2.IntervalMinutes != 120 || got2.Enabled {
		t.Fatalf("更新未生效: %+v", got2)
	}

	// 停用后不应出现在 Due 里。
	due2, _ := store.Due(ctx, 50)
	for _, sc := range due2 {
		if sc.ID == id {
			t.Fatal("已停用的计划不应到期")
		}
	}

	// MarkRun 推进 next_run_at 并记录任务。
	next := time.Now().Add(2 * time.Hour)
	if err := store.MarkRun(ctx, id, 12345, next, ""); err != nil {
		t.Fatalf("mark run: %v", err)
	}
	got3, _ := store.Get(ctx, id)
	if got3.LastTaskID != 12345 {
		t.Fatalf("last_task_id 未写入: %+v", got3)
	}
	if !got3.NextRunAt.After(time.Now().Add(90 * time.Minute)) {
		t.Fatalf("next_run_at 未推进: %v", got3.NextRunAt)
	}

	// 失败记录。
	if err := store.MarkRun(ctx, id, 0, next, "boom"); err != nil {
		t.Fatalf("mark run err: %v", err)
	}
	got4, _ := store.Get(ctx, id)
	if got4.LastError != "boom" {
		t.Fatalf("last_error 未写入: %+v", got4)
	}

	if err := store.Delete(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got5, _ := store.Get(ctx, id); got5 != nil {
		t.Fatal("删除后不应还能取到")
	}
}

func TestTaskScheduleListOrder(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	defer d.Close()
	ctx := context.Background()
	store := d.TaskSchedules()
	prefix := "schedule-order-test"
	_, _ = d.Exec(`DELETE FROM task_schedules WHERE name LIKE $1`, prefix+"%")
	defer d.Exec(`DELETE FROM task_schedules WHERE name LIKE $1`, prefix+"%")

	for i := 0; i < 3; i++ {
		if _, err := store.Create(ctx, TaskSchedule{
			Name: prefix + string(rune('a'+i)), Goal: "g", IntervalMinutes: 60,
		}); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var ids []int64
	for _, sc := range list {
		if len(sc.Name) >= len(prefix) && sc.Name[:len(prefix)] == prefix {
			ids = append(ids, sc.ID)
		}
	}
	if len(ids) != 3 {
		t.Fatalf("应返回 3 条，实际 %d", len(ids))
	}
	// 最新在前（id DESC）。
	for i := 1; i < len(ids); i++ {
		if ids[i] > ids[i-1] {
			t.Fatalf("List 应按 id 降序: %v", ids)
		}
	}
}
