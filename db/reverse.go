package db

// 客户端/二进制逆向样本的持久化（P0）。样本文件由 server 落盘，这里只登记
// 元数据、静态分析结果与敏感信息候选；样本永不在此层被执行。

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type ReverseSample struct {
	ID              int64           `json:"id"`
	TaskID          int64           `json:"task_id"`
	Filename        string          `json:"filename"`
	SHA256          string          `json:"sha256"`
	SizeBytes       int64           `json:"size_bytes"`
	Kind            string          `json:"kind"`
	Status          string          `json:"status"`
	Metadata        json.RawMessage `json:"metadata"`
	Error           string          `json:"error,omitempty"`
	StoragePath     string          `json:"-"`
	DecompileEngine string          `json:"decompile_engine,omitempty"`
	DecompileStatus string          `json:"decompile_status,omitempty"`
	DecompileDir    string          `json:"-"`
	DecompileError  string          `json:"decompile_error,omitempty"`
	DecompiledAt    *time.Time      `json:"decompiled_at,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	AnalyzedAt      *time.Time      `json:"analyzed_at,omitempty"`
	SecretCount     int             `json:"secret_count"`
}

type ReverseSecret struct {
	ID         int64   `json:"id"`
	SampleID   int64   `json:"sample_id"`
	Rule       string  `json:"rule"`
	Match      string  `json:"match"`
	Context    string  `json:"context"`
	Offset     int64   `json:"offset"`
	Section    string  `json:"section,omitempty"`
	Source     string  `json:"source,omitempty"` // binary | decompile
	Confidence float64 `json:"confidence"`
}

const reverseSampleCols = `id,task_id,filename,sha256,size_bytes,kind,status,metadata,error,storage_path,
	decompile_engine,decompile_status,decompile_dir,decompile_error,decompiled_at,created_at,analyzed_at,
	(SELECT count(*) FROM reverse_secrets s WHERE s.sample_id=reverse_samples.id)`

func scanReverseSample(sc interface{ Scan(...any) error }) (*ReverseSample, error) {
	var s ReverseSample
	var meta []byte
	var analyzed, decompiled sql.NullTime
	if err := sc.Scan(&s.ID, &s.TaskID, &s.Filename, &s.SHA256, &s.SizeBytes, &s.Kind, &s.Status,
		&meta, &s.Error, &s.StoragePath, &s.DecompileEngine, &s.DecompileStatus, &s.DecompileDir,
		&s.DecompileError, &decompiled, &s.CreatedAt, &analyzed, &s.SecretCount); err != nil {
		return nil, err
	}
	if len(meta) == 0 {
		meta = []byte(`{}`)
	}
	s.Metadata = json.RawMessage(meta)
	if analyzed.Valid {
		t := analyzed.Time
		s.AnalyzedAt = &t
	}
	if decompiled.Valid {
		t := decompiled.Time
		s.DecompiledAt = &t
	}
	return &s, nil
}

// InsertReverseSample 登记样本；同任务同 SHA-256 已存在时返回 inserted=false 并
// 回填已有记录（调用方据此跳过重复分析）。
func (d *DB) InsertReverseSample(ctx context.Context, s *ReverseSample) (bool, error) {
	if len(s.Metadata) == 0 {
		s.Metadata = json.RawMessage(`{}`)
	}
	err := d.QueryRowContext(ctx, `
INSERT INTO reverse_samples(task_id,filename,sha256,size_bytes,kind,status,metadata,error,storage_path)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (task_id,sha256) DO NOTHING
RETURNING id,created_at`,
		s.TaskID, s.Filename, s.SHA256, s.SizeBytes, s.Kind, s.Status, s.Metadata, s.Error, s.StoragePath).
		Scan(&s.ID, &s.CreatedAt)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	existing, err := d.reverseSampleByTaskSHA(s.TaskID, s.SHA256)
	if err != nil {
		return false, err
	}
	*s = *existing
	return false, nil
}

func (d *DB) reverseSampleByTaskSHA(taskID int64, sha string) (*ReverseSample, error) {
	row := d.QueryRow(`SELECT `+reverseSampleCols+` FROM reverse_samples WHERE task_id=$1 AND sha256=$2`, taskID, sha)
	return scanReverseSample(row)
}

func (d *DB) GetReverseSample(id int64) (*ReverseSample, error) {
	row := d.QueryRow(`SELECT `+reverseSampleCols+` FROM reverse_samples WHERE id=$1`, id)
	return scanReverseSample(row)
}

func (d *DB) ListReverseSamples(taskID int64) ([]*ReverseSample, error) {
	rows, err := d.Query(`SELECT `+reverseSampleCols+` FROM reverse_samples WHERE task_id=$1 ORDER BY id DESC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ReverseSample
	for rows.Next() {
		s, err := scanReverseSample(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (d *DB) UpdateReverseSampleAnalysis(id int64, kind, status string, metadata json.RawMessage, errMsg string) error {
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	_, err := d.Exec(`
UPDATE reverse_samples
SET kind=$2, status=$3, metadata=$4, error=$5,
    analyzed_at=CASE WHEN $3='analyzed' THEN now() ELSE analyzed_at END
WHERE id=$1`, id, kind, status, metadata, errMsg)
	return err
}

// ReplaceReverseSecrets 原子替换某样本某一来源的全部秘密候选（重新分析/反编译
// 时只清理自己那一批，二进制扫描结果与反编译结果互不覆盖）。
func (d *DB) ReplaceReverseSecrets(ctx context.Context, sampleID int64, source string, secrets []ReverseSecret) error {
	if source == "" {
		source = "binary"
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `DELETE FROM reverse_secrets WHERE sample_id=$1 AND source=$2`, sampleID, source); err != nil {
		return err
	}
	for _, s := range secrets {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO reverse_secrets(sample_id,rule,match,context,offset_bytes,section,source,confidence)
VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
			sampleID, s.Rule, s.Match, s.Context, s.Offset, s.Section, source, s.Confidence); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) ListReverseSecrets(sampleID int64) ([]ReverseSecret, error) {
	rows, err := d.Query(`
SELECT id,sample_id,rule,match,context,offset_bytes,section,source,confidence
FROM reverse_secrets WHERE sample_id=$1 ORDER BY confidence DESC, id LIMIT 500`, sampleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReverseSecret
	for rows.Next() {
		var s ReverseSecret
		if err := rows.Scan(&s.ID, &s.SampleID, &s.Rule, &s.Match, &s.Context, &s.Offset, &s.Section, &s.Source, &s.Confidence); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// UpdateReverseSampleDecompile 记录一次反编译任务的状态。
func (d *DB) UpdateReverseSampleDecompile(id int64, engine, status, dir, errMsg string) error {
	_, err := d.Exec(`
UPDATE reverse_samples
SET decompile_engine=$2, decompile_status=$3, decompile_dir=$4, decompile_error=$5,
    decompiled_at=CASE WHEN $3='done' THEN now() ELSE decompiled_at END
WHERE id=$1`, id, engine, status, dir, errMsg)
	return err
}

// DeleteReverseSample 删除样本并返回其存储路径（调用方负责删文件）。
func (d *DB) DeleteReverseSample(taskID, id int64) (string, error) {
	var path string
	err := d.QueryRow(`DELETE FROM reverse_samples WHERE id=$1 AND task_id=$2 RETURNING storage_path`, id, taskID).Scan(&path)
	return path, err
}
