package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// ═══ M-07：资产快照与变更检测 ═══
//
// 资产图只有「当前状态」：回答不了「这次比上次多了什么子域、哪个服务换了指纹」。
// 快照把某个范围的状态按时间点固化，diff 出新增/消失/变化。用途：
//   - 新出现的资产直接进「未测资产」视角，可只对增量派任务；
//   - 新指纹 × 最新漏洞情报（快通道）才能发现「刚爆的 CVE 正好命中刚上线的组件」。
//
// 指纹口径要稳定：只取会影响判断的字段（技术栈/状态码/标题/服务名/开放端口），
// 不含 last_seen/updated_at 这类随时间变化的列，否则每次 diff 都会全量「变化」。

// AssetSnapshot 是一次快照的元信息。
type AssetSnapshot struct {
	ID         int64     `json:"id"`
	ScopeKey   string    `json:"scope_key"`
	AssetCount int       `json:"asset_count"`
	Digest     string    `json:"digest"`
	CreatedAt  time.Time `json:"created_at"`
}

// AssetDiffItem 是新增或消失的一项。
type AssetDiffItem struct {
	AssetID int64  `json:"asset_id"`
	Type    string `json:"type"`
	Label   string `json:"label"`
}

// AssetDiffChange 是同一资产但指纹变化（如组件升级、标题变化、端口变化）。
type AssetDiffChange struct {
	AssetID     int64  `json:"asset_id"`
	Type        string `json:"type"`
	Label       string `json:"label"`
	OldFinger   string `json:"old_fingerprint"`
	NewFinger   string `json:"new_fingerprint"`
}

// AssetDiff 是两次快照之间的差异。
type AssetDiff struct {
	BaseID    int64             `json:"base_id"`
	NewID     int64             `json:"new_id"`
	BaseAt    time.Time         `json:"base_at"`
	NewAt     time.Time         `json:"new_at"`
	Added     []AssetDiffItem   `json:"added"`
	Removed   []AssetDiffItem   `json:"removed"`
	Changed   []AssetDiffChange `json:"changed"`
}

// AssetSnapshotStore 读写 asset_snapshots / asset_snapshot_items。
type AssetSnapshotStore struct{ db *DB }

func (d *DB) AssetSnapshots() *AssetSnapshotStore { return &AssetSnapshotStore{db: d} }

// snapshotLabelExpr 是资产的可读标签口径，与资产列表页保持一致。
const snapshotLabelExpr = `COALESCE(NULLIF(a.domain,''), NULLIF(a.url,''), NULLIF(a.ip,''), NULLIF(a.app_name,''), NULLIF(a.root_domain,''), '#'||a.id::text)`

// snapshotFingerExpr 是变更判定用的稳定指纹：只含影响判断的字段。
const snapshotFingerExpr = `md5(COALESCE(array_to_string(a.technologies,','),'') || '|' ||
    COALESCE(a.status_code::text,'') || '|' ||
    COALESCE(a.page_title,'') || '|' ||
    COALESCE(a.service_name,'') || '|' ||
    COALESCE(a.open_ports::text,'') || '|' ||
    COALESCE(a.type,''))`

// TakeSnapshot 采集一次快照并返回 id。scopeKey 为空表示全站，'company:<id>' 表示
// 单个企业。同一范围内多次采集是幂等的（只追加新快照，不改历史）。
func (s *AssetSnapshotStore) TakeSnapshot(ctx context.Context, scopeKey string) (*AssetSnapshot, error) {
	scopeKey = strings.TrimSpace(scopeKey)
	where := `WHERE true`
	args := []any{}
	if cid, ok := parseCompanyScope(scopeKey); ok {
		where = `WHERE a.company_id=$2`
		args = append(args, cid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var snapID int64
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO asset_snapshots(scope_key) VALUES ($1) RETURNING id`, scopeKey).Scan(&snapID); err != nil {
		return nil, err
	}
	// 一次 INSERT ... SELECT 把当前资产写进 items，避免逐行往返。
	if _, err := tx.ExecContext(ctx, `
INSERT INTO asset_snapshot_items(snapshot_id, asset_id, type, label, fingerprint)
SELECT $1, a.id, a.type, `+snapshotLabelExpr+`, `+snapshotFingerExpr+`
FROM assets a `+where, append([]any{snapID}, args...)...); err != nil {
		return nil, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM asset_snapshot_items WHERE snapshot_id=$1`, snapID).Scan(&count); err != nil {
		return nil, err
	}
	// digest 是条目集合的稳定摘要：同内容两次快照得到同一 digest，便于快速判断
	// 「这一轮有没有变化」（不必每次都跑完整 diff）。
	rows, err := tx.QueryContext(ctx,
		`SELECT asset_id, type, label, fingerprint FROM asset_snapshot_items WHERE snapshot_id=$1 ORDER BY asset_id`, snapID)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	for rows.Next() {
		var id int64
		var typ, label, fp string
		if err := rows.Scan(&id, &typ, &label, &fp); err != nil {
			rows.Close()
			return nil, err
		}
		fmt.Fprintf(h, "%d|%s|%s|%s\n", id, typ, label, fp)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	digest := hex.EncodeToString(h.Sum(nil))[:16]
	if _, err := tx.ExecContext(ctx, `UPDATE asset_snapshots SET asset_count=$2, digest=$3 WHERE id=$1`,
		snapID, count, digest); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	snap, err := s.Get(ctx, snapID)
	if err != nil {
		return nil, err
	}
	return snap, nil
}

// parseCompanyScope 解析 'company:<id>'。
func parseCompanyScope(scopeKey string) (int64, bool) {
	const p = "company:"
	if !strings.HasPrefix(scopeKey, p) {
		return 0, false
	}
	var id int64
	if _, err := fmt.Sscanf(scopeKey[len(p):], "%d", &id); err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// Get 按 id 取快照元信息。
func (s *AssetSnapshotStore) Get(ctx context.Context, id int64) (*AssetSnapshot, error) {
	var snap AssetSnapshot
	err := s.db.QueryRowContext(ctx,
		`SELECT id, scope_key, asset_count, digest, created_at FROM asset_snapshots WHERE id=$1`, id).
		Scan(&snap.ID, &snap.ScopeKey, &snap.AssetCount, &snap.Digest, &snap.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &snap, err
}

// List 返回某范围的快照（最新在前）。
func (s *AssetSnapshotStore) List(ctx context.Context, scopeKey string, limit int) ([]AssetSnapshot, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, scope_key, asset_count, digest, created_at FROM asset_snapshots
WHERE scope_key=$1 ORDER BY id DESC LIMIT $2`, strings.TrimSpace(scopeKey), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AssetSnapshot{}
	for rows.Next() {
		var snap AssetSnapshot
		if err := rows.Scan(&snap.ID, &snap.ScopeKey, &snap.AssetCount, &snap.Digest, &snap.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, snap)
	}
	return out, rows.Err()
}

// Diff 比较两次快照。baseID <= 0 时自动取 newID 之前最近的一次同范围快照。
func (s *AssetSnapshotStore) Diff(ctx context.Context, baseID, newID int64) (*AssetDiff, error) {
	newSnap, err := s.Get(ctx, newID)
	if err != nil {
		return nil, err
	}
	if newSnap == nil {
		return nil, sql.ErrNoRows
	}
	if baseID <= 0 {
		if err := s.db.QueryRowContext(ctx, `
SELECT id FROM asset_snapshots WHERE scope_key=$1 AND id<$2 ORDER BY id DESC LIMIT 1`,
			newSnap.ScopeKey, newID).Scan(&baseID); err != nil {
			return nil, fmt.Errorf("没有更早的快照可比对（先采集两次快照再 diff）")
		}
	}
	baseSnap, err := s.Get(ctx, baseID)
	if err != nil {
		return nil, err
	}
	if baseSnap == nil {
		return nil, sql.ErrNoRows
	}
	out := &AssetDiff{BaseID: baseID, NewID: newID, BaseAt: baseSnap.CreatedAt, NewAt: newSnap.CreatedAt,
		Added: []AssetDiffItem{}, Removed: []AssetDiffItem{}, Changed: []AssetDiffChange{}}

	// 新增：在 new 里但不在 base 里。
	if err := s.scanDiffItems(ctx, out, `added`, baseID, newID); err != nil {
		return nil, err
	}
	if err := s.scanDiffItems(ctx, out, `removed`, baseID, newID); err != nil {
		return nil, err
	}
	// 变化：两边都有但指纹不同。
	rows, err := s.db.QueryContext(ctx, `
SELECT n.asset_id, n.type, n.label, b.fingerprint, n.fingerprint
FROM asset_snapshot_items n
JOIN asset_snapshot_items b ON b.snapshot_id=$1 AND b.asset_id=n.asset_id
WHERE n.snapshot_id=$2 AND b.fingerprint <> n.fingerprint
ORDER BY n.asset_id`, baseID, newID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c AssetDiffChange
		if err := rows.Scan(&c.AssetID, &c.Type, &c.Label, &c.OldFinger, &c.NewFinger); err != nil {
			return nil, err
		}
		out.Changed = append(out.Changed, c)
	}
	return out, rows.Err()
}

func (s *AssetSnapshotStore) scanDiffItems(ctx context.Context, out *AssetDiff, kind string, baseID, newID int64) error {
	var q string
	switch kind {
	case "added":
		q = `SELECT n.asset_id, n.type, n.label FROM asset_snapshot_items n
WHERE n.snapshot_id=$2 AND NOT EXISTS (
  SELECT 1 FROM asset_snapshot_items b WHERE b.snapshot_id=$1 AND b.asset_id=n.asset_id)
ORDER BY n.asset_id`
	case "removed":
		q = `SELECT b.asset_id, b.type, b.label FROM asset_snapshot_items b
WHERE b.snapshot_id=$1 AND NOT EXISTS (
  SELECT 1 FROM asset_snapshot_items n WHERE n.snapshot_id=$2 AND n.asset_id=b.asset_id)
ORDER BY b.asset_id`
	default:
		return fmt.Errorf("unknown diff kind %q", kind)
	}
	rows, err := s.db.QueryContext(ctx, q, baseID, newID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var it AssetDiffItem
		if err := rows.Scan(&it.AssetID, &it.Type, &it.Label); err != nil {
			return err
		}
		if kind == "added" {
			out.Added = append(out.Added, it)
		} else {
			out.Removed = append(out.Removed, it)
		}
	}
	return rows.Err()
}
