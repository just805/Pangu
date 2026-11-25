package db

import (
	"context"
	"fmt"
	"strconv"
)

// ═══ M-03：角色权限矩阵 ═══
//
// 越权测试（IDOR / 垂直越权 / 未授权访问）的前提是「有两个身份、知道谁能访问什么」。
// 之前这个前提只存在于模型的工作记忆里：每条越权意图都要重新从流量猜「哪个角色
// 该不该有权限」，猜完就丢，缺口也看不见。本表把它变成任务级的结构化数据。
//
// 三条设计约束：
//  1. 按 task_id 沉淀、随任务级联删除——矩阵是这次授权的产物，不是全局资产；
//  2. role × host × method × path 唯一，重复上报覆盖更新，不堆重复行；
//  3. expected / observed 都是三态（allow | deny | unknown），不一致只作线索，
//     确认仍要 traffic_id 指向的真实请求响应——与平台的证据闸门同一口径。

// AuthzEntry 是矩阵的一行。
type AuthzEntry struct {
	ID         int64  `json:"id"`
	Role       string `json:"role"`
	Host       string `json:"host,omitempty"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Expected   string `json:"expected"`
	Observed   string `json:"observed"`
	StatusCode int    `json:"status_code,omitempty"`
	TrafficID  string `json:"traffic_id,omitempty"`
	Note       string `json:"note,omitempty"`
	IntentID   int64  `json:"intent_id,omitempty"`
}

// AuthzStats 是矩阵的聚合视图，供 planner 判断「还差哪些组合没测」。
type AuthzStats struct {
	Total      int `json:"total"`       // 条目总数
	Allowed    int `json:"allowed"`     // observed=allow
	Denied     int `json:"denied"`      // observed=deny
	Unknown    int `json:"unknown"`     // observed=unknown（还没测）
	Suspicious int `json:"suspicious"`  // expected≠observed 且两者都不为 unknown
	NoEvidence int `json:"no_evidence"` // 有实测结论但没有 traffic_id
}

// AuthzMatrixStore 读写 authz_matrix。
type AuthzMatrixStore struct{ db *DB }

func (d *DB) AuthzMatrix() *AuthzMatrixStore { return &AuthzMatrixStore{db: d} }

// Authz 返回与当前探索图同一数据库的权限矩阵存储。矩阵按任务归属，但通过探索图
// 句柄访问，省得再往 ToolSet 注入一个新 store（工具侧只持有 t.ts）。
func (s *ExplorationStore) Authz() *AuthzMatrixStore { return &AuthzMatrixStore{db: s.db} }

// Upsert 写入/覆盖一条矩阵记录，返回行 id。
func (s *AuthzMatrixStore) Upsert(ctx context.Context, taskID, expID int64, e AuthzEntry) (int64, error) {
	if taskID <= 0 || expID <= 0 {
		return 0, fmt.Errorf("authz_matrix 需要任务与探索图上下文")
	}
	var id int64
	err := s.db.QueryRowContext(ctx, `
INSERT INTO authz_matrix (task_id, exploration_id, role, host, method, path,
                          expected, observed, status_code, traffic_id, note, intent_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (task_id, role, host, method, path) DO UPDATE SET
  expected    = EXCLUDED.expected,
  observed    = EXCLUDED.observed,
  status_code = EXCLUDED.status_code,
  traffic_id  = EXCLUDED.traffic_id,
  note        = EXCLUDED.note,
  intent_id   = EXCLUDED.intent_id,
  updated_at  = now()
RETURNING id`,
		taskID, expID, e.Role, e.Host, e.Method, e.Path,
		e.Expected, e.Observed, nullableInt(e.StatusCode), e.TrafficID, e.Note, nullableInt64(e.IntentID),
	).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// List 返回某任务的矩阵条目（按角色、path 排序，便于模型对照）。
// role 非空时只返回该角色。
func (s *AuthzMatrixStore) List(taskID int64, role string, limit int) ([]AuthzEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q := `SELECT id, role, host, method, path, expected, observed,
	             COALESCE(status_code,0), traffic_id, note, COALESCE(intent_id,0)
	      FROM authz_matrix WHERE task_id=$1`
	args := []any{taskID}
	if role != "" {
		q += ` AND role=$2`
		args = append(args, role)
	}
	q += ` ORDER BY role, path, method LIMIT ` + strconv.Itoa(limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuthzEntry
	for rows.Next() {
		var e AuthzEntry
		if err := rows.Scan(&e.ID, &e.Role, &e.Host, &e.Method, &e.Path,
			&e.Expected, &e.Observed, &e.StatusCode, &e.TrafficID, &e.Note, &e.IntentID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Stats 聚合矩阵状态。查询失败返回零值 + 错误，由调用方决定是否降级。
func (s *AuthzMatrixStore) Stats(taskID int64) (AuthzStats, error) {
	var st AuthzStats
	err := s.db.QueryRow(`
SELECT count(*),
       count(*) FILTER (WHERE observed='allow'),
       count(*) FILTER (WHERE observed='deny'),
       count(*) FILTER (WHERE observed='unknown'),
       count(*) FILTER (WHERE expected<>'unknown' AND observed<>'unknown' AND expected<>observed),
       count(*) FILTER (WHERE observed<>'unknown' AND traffic_id='')
FROM authz_matrix WHERE task_id=$1`, taskID).Scan(
		&st.Total, &st.Allowed, &st.Denied, &st.Unknown, &st.Suspicious, &st.NoEvidence)
	return st, err
}

// DeleteByTask 清理某任务的矩阵（任务物理删除时的兜底；schema 未加外键，
// 因为 tasks 的删除路径不一定都走级联）。
func (s *AuthzMatrixStore) DeleteByTask(ctx context.Context, taskID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM authz_matrix WHERE task_id=$1`, taskID)
	return err
}

func nullableInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}
