package db

import (
	"context"
	"fmt"
)

// ── 跨任务经验沉淀（P1）──────────────────────────────────────────────────────
//
// 任务收官时规则抽取「有效手法 / 受限教训」，新任务按企业交集注入 planner 态势。
// 经验跟企业走（company_ids 数组对齐 task_companies），不跨客户泄密。行是幂等
// 重写的：同一任务重复收官（恢复后再终态）以最新抽取为准。

// TaskLesson 是一条沉淀下来的经验。
type TaskLesson struct {
	Kind           string `json:"kind"` // "technique"（有效手法）| "lesson"（受限教训）
	Text           string `json:"text"`
	SourceIntentID int64  `json:"source_intent_id,omitempty"`
}

// ReplaceTaskLessons 幂等写回一个任务的经验（先删后插）。companyIDs 为空时行
// 归属全站（company_ids='{}'，任何任务都可查到——纯通用经验）。
func (d *DB) ReplaceTaskLessons(ctx context.Context, taskID int64, companyIDs []int64, lessons []TaskLesson) error {
	if d == nil {
		return fmt.Errorf("数据库未就绪")
	}
	if len(lessons) == 0 {
		return nil
	}
	// nil slice 会被 pgx 绑成 NULL、撞 NOT NULL 约束——规范化成空数组（全站通用行）。
	if companyIDs == nil {
		companyIDs = []int64{}
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM task_lessons WHERE task_id=$1`, taskID); err != nil {
		return err
	}
	for _, l := range lessons {
		if l.Text == "" {
			continue
		}
		if _, err := tx.Exec(
			`INSERT INTO task_lessons(task_id,company_ids,kind,text,source_intent_id) VALUES ($1,$2,$3,$4,$5)`,
			taskID, companyIDs, l.Kind, l.Text, l.SourceIntentID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// TaskLessonsForCompanies 返回查询方可见的最近经验（≤limit，最新在前）：
//   - 带企业归属的行只对有交集的任务可见（经验跟企业走，不跨客户泄密）；
//   - 无归属（company_ids='{}'）的通用行对所有人都可见；
//   - 查询方自身无企业归属（内部任务）→ 可见全部。
//
// excludeTaskID > 0 时排除自身任务（planner 注入场景：不给本任务自己的经验回声）。
func (d *DB) TaskLessonsForCompanies(ctx context.Context, companyIDs []int64, excludeTaskID int64, limit int) ([]TaskLesson, error) {
	if d == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 5
	}
	// nil slice 会被 pgx 绑成 NULL——先规范化成空数组（= 查询方无归属，可见全部）。
	if companyIDs == nil {
		companyIDs = []int64{}
	}
	q := `SELECT kind, text, COALESCE(source_intent_id,0) FROM task_lessons
	      WHERE (company_ids = '{}' OR cardinality($1::bigint[]) = 0 OR company_ids && $1::bigint[])`
	args := []any{companyIDs}
	if excludeTaskID > 0 {
		q += ` AND (task_id IS NULL OR task_id <> $2)`
		args = append(args, excludeTaskID)
	}
	q += ` ORDER BY id DESC LIMIT ` + fmt.Sprint(limit)
	rows, err := d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskLesson{}
	for rows.Next() {
		var l TaskLesson
		if err := rows.Scan(&l.Kind, &l.Text, &l.SourceIntentID); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
