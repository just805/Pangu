package db

import (
	"context"
	"log"
	"strconv"
)

// ── 工具调用延迟画像（P1）────────────────────────────────────────────────────
//
// 一次工具调用一行（agent 侧 PostToolUse 与 Pre 配对耗时后落笔），per-(role,tool)
// 聚合 count/avg/p95/max/错误率。与 tool_usage（调用次数账本，meterTool 前置插行）
// 刻意分表：口径不同（次数 vs 耗时）、写入路径不同。常开轻量账本模式：成功失败
// 都记、单行 insert、现查现聚合（量级 = 工具调用数，实测 5.6 万次/12 任务）。

// ToolLatencyRow 是一次工具调用的延迟记录。
type ToolLatencyRow struct {
	TaskID        int64
	ExplorationID int64
	IntentID      int64
	Role          string
	Tool          string
	LatencyMS     int64
	IsError       bool
}

// InsertToolLatency 落一行延迟记录。失败只记日志不返回错误——画像纯旁路，
// 绝不能把工具调用主流程拖挂。
func (d *DB) InsertToolLatency(ctx context.Context, r ToolLatencyRow) {
	if d == nil || r.Tool == "" {
		return
	}
	_, err := d.ExecContext(ctx,
		`INSERT INTO tool_latency(task_id,exploration_id,intent_id,role,tool,latency_ms,is_error) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		r.TaskID, r.ExplorationID, r.IntentID, r.Role, r.Tool, r.LatencyMS, r.IsError)
	if err != nil {
		log.Printf("[tool-latency] insert: %v", err)
	}
}

// ToolLatencyStat 是一个 (role, tool) 组合的延迟画像。
type ToolLatencyStat struct {
	Role    string  `json:"role"`
	Tool    string  `json:"tool"`
	Calls   int64   `json:"calls"`
	AvgMS   float64 `json:"avg_ms"`
	P95MS   float64 `json:"p95_ms"`
	MaxMS   int64   `json:"max_ms"`
	Errors  int64   `json:"errors"`
	TotalMS int64   `json:"total_ms"`
}

// ToolLatencyAggregate 按 (role, tool) 聚合延迟画像，总耗时最长的在前。taskID>0
// 时限定单任务，否则全站。p95 用 percentile_cont（连续插值）。
func (d *DB) ToolLatencyAggregate(ctx context.Context, taskID int64, limit int) ([]ToolLatencyStat, error) {
	if d == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT role, tool, COUNT(*), AVG(latency_ms),
	          COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms), 0),
	          MAX(latency_ms), COUNT(*) FILTER (WHERE is_error), SUM(latency_ms)
	      FROM tool_latency`
	args := []any{}
	if taskID > 0 {
		q += ` WHERE task_id=$1`
		args = append(args, taskID)
	}
	// limit 是内部 int（无注入面），直接拼字面量；不进 args——占位符与参数必须
	// 一一对应，多传一个会被 pgx 拒绝。
	q += ` GROUP BY role, tool ORDER BY SUM(latency_ms) DESC LIMIT ` + strconv.Itoa(limit)
	rows, err := d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ToolLatencyStat{}
	for rows.Next() {
		var s ToolLatencyStat
		if err := rows.Scan(&s.Role, &s.Tool, &s.Calls, &s.AvgMS, &s.P95MS, &s.MaxMS, &s.Errors, &s.TotalMS); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
