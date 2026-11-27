package server

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/just805/pangu/db"
)

// TestScheduleShouldDefer 钉住「上一轮未结束不叠加」的判定：
// 非终态 → 顺延；终态 → 允许触发；空状态（任务已删/查不到）→ 不阻塞，
// 否则一条历史脏 LastTaskID 会让计划永久停摆。
func TestScheduleShouldDefer(t *testing.T) {
	for _, st := range []string{"running", "created", "paused", "queued"} {
		if !scheduleShouldDefer(st) {
			t.Errorf("状态 %q 应顺延（上一轮还没结束）", st)
		}
	}
	for _, st := range []string{"done", "failed", "timeout", ""} {
		if scheduleShouldDefer(st) {
			t.Errorf("状态 %q 不应顺延", st)
		}
	}
}

// TestSchedulesNeverTriggerRetests 是「复测纯手动」这条决策的守门测试。
//
// 调度器最容易在后续改动中被顺手加上的，就是「发现修好了就自动复测」这类看似
// 合理的能力。它不会报错、不会告警，只会在某个半夜自动打一遍客户系统。所以用
// 源码扫描把它钉死：schedules.go 里不得出现任何复测相关的标识符或调用。
func TestSchedulesNeverTriggerRetests(t *testing.T) {
	b, err := os.ReadFile("schedules.go")
	if err != nil {
		t.Fatalf("读取 schedules.go: %v", err)
	}
	src := stripLineComments(string(b))
	for _, kw := range []string{
		"FindingRetest", "RetestStore", ".Retests(", "TriggerRetest", "retest_id", "finding_retests",
	} {
		if strings.Contains(src, kw) {
			t.Errorf("调度器不得涉及复测：出现 %q。复测是纯手动决策，调度只能新建侦察/攻击面任务。", kw)
		}
	}
	// 触发路径必须走统一准入：先 CreateTaskWithOptions 建任务，再 launchTask 启动。
	// 绕过其中任何一步都会跳过并发上限、FIFO 排队与第 0 轮目标拆解。
	if !strings.Contains(src, "CreateTaskWithOptions") {
		t.Error("调度创建任务必须走 Manager.CreateTaskWithOptions")
	}
	if !strings.Contains(src, "s.launchTask(") {
		t.Error("调度启动任务必须走 launchTask（统一准入）")
	}
}

// stripLineComments 去掉整行注释与行尾注释，供源码守门测试使用。
// 注释里说明「不碰 finding_retests」是正当的，不该被当成违规代码；
// 这里不追求完整的 Go 词法（无法处理字符串内的 //），守门场景够用。
func stripLineComments(src string) string {
	lines := strings.Split(src, "\n")
	for i, ln := range lines {
		if idx := strings.Index(ln, "//"); idx >= 0 {
			lines[i] = ln[:idx]
		}
	}
	return strings.Join(lines, "\n")
}

// TestRunDueSchedulesDefersWhenLastTaskStillRunning 是「不叠加」的集成测试。
//
// 特意把任务从内存里摘掉，模拟进程重启后的真实场景：只看内存的实现会返回空
// 状态、把「还在跑」误判成「已结束」，于是重启后第一个 tick 就再起一个任务，
// 与上一轮叠加打同一个目标。这正是本模块最该避免的形态。
func TestRunDueSchedulesDefersWhenLastTaskStillRunning(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	defer m.Close()
	pg := m.PG()
	ctx := context.Background()

	const desc = "sched-defer-test"
	t.Cleanup(func() {
		_, _ = pg.Exec(`DELETE FROM tasks WHERE description=$1`, desc)
		_, _ = pg.Exec(`DELETE FROM task_schedules WHERE name=$1`, desc)
	})
	_, _ = pg.Exec(`DELETE FROM tasks WHERE description=$1`, desc)
	_, _ = pg.Exec(`DELETE FROM task_schedules WHERE name=$1`, desc)

	task, err := m.CreateTaskWithOptions(desc, "sched-defer-goal", db.TaskCreateOptions{Name: desc})
	if err != nil {
		t.Fatalf("创建任务: %v", err)
	}
	// 模拟重启：内存没有，DB 里非终态。
	m.mu.Lock()
	delete(m.tasks, task.ID)
	m.mu.Unlock()
	if _, err := pg.Exec(`UPDATE tasks SET status='running' WHERE id=$1`, task.ID); err != nil {
		t.Fatalf("置任务状态: %v", err)
	}

	store := pg.TaskSchedules()
	planID, err := store.Create(ctx, db.TaskSchedule{
		Name: desc, Description: desc, Goal: "sched-defer-goal",
		IntervalMinutes: 60, Enabled: true,
	})
	if err != nil {
		t.Fatalf("创建计划: %v", err)
	}
	// 置为到期，并指向上一条仍在跑的任务。
	if _, err := pg.Exec(`
UPDATE task_schedules SET next_run_at = now() - interval '5 minutes', last_task_id = $1
WHERE id = $2`, task.ID, planID); err != nil {
		t.Fatalf("置计划为到期: %v", err)
	}

	countTasks := func() int {
		var n int
		if err := pg.QueryRow(`SELECT count(*) FROM tasks WHERE description=$1`, desc).Scan(&n); err != nil {
			t.Fatalf("统计任务: %v", err)
		}
		return n
	}
	before := countTasks()

	srv := newAdmissionTestServer(m, nil)
	srv.runDueSchedules(ctx)

	if after := countTasks(); after != before {
		t.Errorf("上一轮任务仍在跑时不应新建任务：%d → %d（叠加执行）", before, after)
	}
	plan, err := store.Get(ctx, planID)
	if err != nil || plan == nil {
		t.Fatalf("读取计划: %v", err)
	}
	if plan.LastTaskID == 0 {
		t.Error("顺延不应清空 last_task_id")
	}
	if !plan.NextRunAt.After(time.Now()) {
		t.Errorf("顺延后 next_run_at 应在将来，实际 %v", plan.NextRunAt)
	}
}
