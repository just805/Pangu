package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ═══ M-06：周期任务调度 ═══
//
// 边界（重要）：本表只调度「新建侦察/攻击面类任务」。平台已决策「复测纯手动，
// 禁止自动复测/提醒」，所以调度器永远不碰 finding_retests——这是硬边界，不是
// 可配置项。
//
// 为什么不让调度器直接杀进引擎：触发时统一走 server.launchTask → admitTask，
// 天然继承并发上限、FIFO 排队与目标拆解，不绕过任何准入逻辑。

// TaskSchedule 是一条周期任务计划。
type TaskSchedule struct {
	ID                   int64     `json:"id"`
	Name                 string    `json:"name"`
	Description          string    `json:"description"`
	Goal                 string    `json:"goal"`
	IntervalMinutes      int       `json:"interval_minutes"`
	Enabled              bool      `json:"enabled"`
	CompanyIDs           []int64   `json:"company_ids"`
	TimeoutSeconds       int       `json:"timeout_seconds"`
	PlanHeartbeatSeconds int       `json:"plan_heartbeat_seconds"`
	CoverageEnabled      bool      `json:"coverage_enabled"`
	NextRunAt            time.Time `json:"next_run_at"`
	LastRunAt            *time.Time `json:"last_run_at,omitempty"`
	LastTaskID           int64     `json:"last_task_id,omitempty"`
	LastError            string    `json:"last_error,omitempty"`
}

// MinScheduleIntervalMinutes 是周期下限：15 分钟。再密就是拿同一个目标反复
// 扫，既没有新信息也容易把目标打挂。
const MinScheduleIntervalMinutes = 15

// Normalize 归一计划字段并校验。
func (s *TaskSchedule) Normalize() error {
	if s.IntervalMinutes <= 0 {
		s.IntervalMinutes = 1440 // 默认每天
	}
	if s.IntervalMinutes < MinScheduleIntervalMinutes {
		return fmt.Errorf("周期不能小于 %d 分钟", MinScheduleIntervalMinutes)
	}
	if s.TimeoutSeconds < 0 {
		s.TimeoutSeconds = 0
	}
	if s.PlanHeartbeatSeconds < 0 {
		s.PlanHeartbeatSeconds = 0
	}
	if len(s.CompanyIDs) > 50 {
		return fmt.Errorf("关联企业过多（上限 50）")
	}
	return nil
}

// TaskScheduleStore 读写 task_schedules。
type TaskScheduleStore struct{ db *DB }

func (d *DB) TaskSchedules() *TaskScheduleStore { return &TaskScheduleStore{db: d} }

// Create 新建计划。next_run_at 默认 now（保存后即到点，由调度循环在下一 tick
// 触发——用户刚建的计划通常希望尽快跑一次）。
func (s *TaskScheduleStore) Create(ctx context.Context, sc TaskSchedule) (int64, error) {
	if err := sc.Normalize(); err != nil {
		return 0, err
	}
	ids, _ := json.Marshal(sc.CompanyIDs)
	var id int64
	err := s.db.QueryRowContext(ctx, `
INSERT INTO task_schedules (name, description, goal, interval_minutes, enabled, company_ids,
                            timeout_seconds, plan_heartbeat_seconds, coverage_enabled, next_run_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9, now())
RETURNING id`,
		sc.Name, sc.Description, sc.Goal, sc.IntervalMinutes, sc.Enabled, ids,
		sc.TimeoutSeconds, sc.PlanHeartbeatSeconds, sc.CoverageEnabled).Scan(&id)
	return id, err
}

// Update 更新计划（不改变 last_* / next_run_at —— 由调度器维护）。
func (s *TaskScheduleStore) Update(ctx context.Context, sc TaskSchedule) error {
	if err := sc.Normalize(); err != nil {
		return err
	}
	ids, _ := json.Marshal(sc.CompanyIDs)
	_, err := s.db.ExecContext(ctx, `
UPDATE task_schedules SET name=$1, description=$2, goal=$3, interval_minutes=$4,
       enabled=$5, company_ids=$6, timeout_seconds=$7, plan_heartbeat_seconds=$8,
       coverage_enabled=$9, updated_at=now()
WHERE id=$10`,
		sc.Name, sc.Description, sc.Goal, sc.IntervalMinutes, sc.Enabled, ids,
		sc.TimeoutSeconds, sc.PlanHeartbeatSeconds, sc.CoverageEnabled, sc.ID)
	return err
}

// Delete 删除计划。
func (s *TaskScheduleStore) Delete(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM task_schedules WHERE id=$1`, id)
	return err
}

// List 返回全部计划（最新在前）。
func (s *TaskScheduleStore) List(ctx context.Context) ([]TaskSchedule, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, description, goal, interval_minutes, enabled, company_ids,
       timeout_seconds, plan_heartbeat_seconds, coverage_enabled,
       next_run_at, last_run_at, COALESCE(last_task_id,0), last_error
FROM task_schedules ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskSchedule{}
	for rows.Next() {
		var sc TaskSchedule
		var ids []byte
		if err := rows.Scan(&sc.ID, &sc.Name, &sc.Description, &sc.Goal, &sc.IntervalMinutes,
			&sc.Enabled, &ids, &sc.TimeoutSeconds, &sc.PlanHeartbeatSeconds, &sc.CoverageEnabled,
			&sc.NextRunAt, &sc.LastRunAt, &sc.LastTaskID, &sc.LastError); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(ids, &sc.CompanyIDs)
		out = append(out, sc)
	}
	return out, rows.Err()
}

// Get 按 id 取一条计划。
func (s *TaskScheduleStore) Get(ctx context.Context, id int64) (*TaskSchedule, error) {
	var sc TaskSchedule
	var ids []byte
	err := s.db.QueryRowContext(ctx, `
SELECT id, name, description, goal, interval_minutes, enabled, company_ids,
       timeout_seconds, plan_heartbeat_seconds, coverage_enabled,
       next_run_at, last_run_at, COALESCE(last_task_id,0), last_error
FROM task_schedules WHERE id=$1`, id).Scan(
		&sc.ID, &sc.Name, &sc.Description, &sc.Goal, &sc.IntervalMinutes,
		&sc.Enabled, &ids, &sc.TimeoutSeconds, &sc.PlanHeartbeatSeconds, &sc.CoverageEnabled,
		&sc.NextRunAt, &sc.LastRunAt, &sc.LastTaskID, &sc.LastError)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(ids, &sc.CompanyIDs)
	return &sc, nil
}

// Due 返回到点且启用的计划（按 next_run_at 升序，限量防一次触发过多）。
func (s *TaskScheduleStore) Due(ctx context.Context, limit int) ([]TaskSchedule, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, description, goal, interval_minutes, enabled, company_ids,
       timeout_seconds, plan_heartbeat_seconds, coverage_enabled,
       next_run_at, last_run_at, COALESCE(last_task_id,0), last_error
FROM task_schedules WHERE enabled AND next_run_at <= now()
ORDER BY next_run_at LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskSchedule{}
	for rows.Next() {
		var sc TaskSchedule
		var ids []byte
		if err := rows.Scan(&sc.ID, &sc.Name, &sc.Description, &sc.Goal, &sc.IntervalMinutes,
			&sc.Enabled, &ids, &sc.TimeoutSeconds, &sc.PlanHeartbeatSeconds, &sc.CoverageEnabled,
			&sc.NextRunAt, &sc.LastRunAt, &sc.LastTaskID, &sc.LastError); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(ids, &sc.CompanyIDs)
		out = append(out, sc)
	}
	return out, rows.Err()
}

// MarkRun 记录一次触发：写入上次任务 id / 时间，并把 next_run_at 推进一个周期。
// next 由调用方决定（正常=now+interval；顺延=now+interval）。
func (s *TaskScheduleStore) MarkRun(ctx context.Context, id, taskID int64, next time.Time, runErr string) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE task_schedules SET last_run_at=now(), last_task_id=NULLIF($2,0), last_error=$3,
       next_run_at=$4, updated_at=now() WHERE id=$1`, id, taskID, runErr, next)
	return err
}

// Touch 只推进 next_run_at（例如上一轮任务还在跑、本次不叠加）。
func (s *TaskScheduleStore) Touch(ctx context.Context, id int64, next time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE task_schedules SET next_run_at=$2, updated_at=now() WHERE id=$1`, id, next)
	return err
}
