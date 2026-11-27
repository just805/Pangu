package db

// 意图终态经验（reflection）：每个意图到达终态（done/exhausted/blocked/stopped）时
// 由引擎确定性写一条，不调用 LLM。planner 每轮看最近几条，worker 启动时看同维度
// 最近几条——避免把同一种失败打法在新意图上再跑一遍（上游 0.4.100 P0-2 同款）。

import (
	"time"
)

// IntentReflection 是一条终态经验。
type IntentReflection struct {
	IntentID      int64     `json:"intent_id"`
	ExplorationID int64     `json:"exploration_id"`
	Dimension     string    `json:"dimension,omitempty"`
	TerminalState string    `json:"terminal_state"`
	StopReason    string    `json:"stop_reason,omitempty"`
	Summary       string    `json:"summary,omitempty"`
	Yields        int       `json:"yields"`
	Assets        []int64   `json:"assets,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// EnsureIntentReflectionsTable 幂等建表（服务启动时调用）。
// 注意：建表已统一到 db/schema.sql 的 §Z 段（2026-11-27）。本函数保留为幂等兜底：
// 不走 Open 的路径（测试直接构造 DB）与老库补列仍需要它。改列时必须同时改 schema.sql，
// db/schema_drift_test.go 会扫 schema.sql 防止两处漂移。
func (d *DB) EnsureIntentReflectionsTable() error {
	_, err := d.Exec(`
CREATE TABLE IF NOT EXISTS intent_reflections (
    intent_id      BIGINT PRIMARY KEY,
    exploration_id BIGINT NOT NULL,
    dimension      TEXT NOT NULL DEFAULT '',
    terminal_state TEXT NOT NULL,
    stop_reason    TEXT,
    summary        TEXT,
    yields         INT NOT NULL DEFAULT 0,
    assets         BIGINT[],
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_intent_reflections_exp ON intent_reflections(exploration_id);
CREATE INDEX IF NOT EXISTS idx_intent_reflections_dim ON intent_reflections(exploration_id, dimension);
`)
	return err
}

// RecordIntentReflection 写入/覆盖一条终态经验（同一意图重复收尾以最后一次为准）。
func (s *ExplorationStore) RecordIntentReflection(r IntentReflection) error {
	_, err := s.db.Exec(`
INSERT INTO intent_reflections (intent_id, exploration_id, dimension, terminal_state, stop_reason, summary, yields, assets, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,now())
ON CONFLICT (intent_id) DO UPDATE SET
    dimension=EXCLUDED.dimension, terminal_state=EXCLUDED.terminal_state,
    stop_reason=EXCLUDED.stop_reason, summary=EXCLUDED.summary,
    yields=EXCLUDED.yields, assets=EXCLUDED.assets, created_at=now()`,
		r.IntentID, s.expID, r.Dimension, r.TerminalState, r.StopReason, r.Summary, r.Yields, r.Assets)
	return err
}

// RecentReflections 返回本探索最近 N 条终态经验（新的在前）。
func (s *ExplorationStore) RecentReflections(limit int) ([]IntentReflection, error) {
	if limit <= 0 {
		limit = 8
	}
	rows, err := s.db.Query(`
SELECT intent_id, exploration_id, COALESCE(dimension,''), terminal_state, COALESCE(stop_reason,''),
       COALESCE(summary,''), yields, assets, created_at
FROM intent_reflections WHERE exploration_id=$1 ORDER BY created_at DESC LIMIT $2`, s.expID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReflections(rows)
}

// ReflectionsByDimension 返回本探索指定维度的最近 N 条经验（worker 启动注入用）。
func (s *ExplorationStore) ReflectionsByDimension(dimension string, limit int) ([]IntentReflection, error) {
	if limit <= 0 {
		limit = 3
	}
	rows, err := s.db.Query(`
SELECT intent_id, exploration_id, COALESCE(dimension,''), terminal_state, COALESCE(stop_reason,''),
       COALESCE(summary,''), yields, assets, created_at
FROM intent_reflections WHERE exploration_id=$1 AND dimension=$2
ORDER BY created_at DESC LIMIT $3`, s.expID, dimension, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReflections(rows)
}

func scanReflections(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]IntentReflection, error) {
	out := []IntentReflection{}
	for rows.Next() {
		var r IntentReflection
		if err := rows.Scan(&r.IntentID, &r.ExplorationID, &r.Dimension, &r.TerminalState,
			&r.StopReason, &r.Summary, &r.Yields, &r.Assets, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
