package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ErrDefaultTemplateDelete 表示试图删除默认模板（先把别的设为默认）。
var ErrDefaultTemplateDelete = errors.New("不能删除默认模板：请先把另一套模板设为默认")

// ═══ M-10：报告模板 ═══
//
// 只存「渲染参数」，不存渲染逻辑——报告仍是手写 OOXML（零第三方文档库）。
// sections 是章节开关：缺省键 = 显示（与历史行为一致），false = 跳过该章。

// ReportTemplate 是一套交付模板。
type ReportTemplate struct {
	ID               int64           `json:"id"`
	Name             string          `json:"name"`
	IsDefault        bool            `json:"is_default"`
	PageHeader       string          `json:"page_header"`
	Classification   string          `json:"classification"`
	CustomerOverride string          `json:"customer_override"`
	Sections         map[string]bool `json:"sections"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

// ReportTemplateStore 读写 report_templates。
type ReportTemplateStore struct{ db *DB }

func (d *DB) ReportTemplates() *ReportTemplateStore { return &ReportTemplateStore{db: d} }

const reportTemplateCols = `id, name, is_default, page_header, classification, customer_override, sections, created_at, updated_at`

func scanReportTemplate(sc interface{ Scan(...any) error }) (*ReportTemplate, error) {
	var t ReportTemplate
	var sections []byte
	if err := sc.Scan(&t.ID, &t.Name, &t.IsDefault, &t.PageHeader, &t.Classification,
		&t.CustomerOverride, &sections, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(sections, &t.Sections)
	if t.Sections == nil {
		t.Sections = map[string]bool{}
	}
	return &t, nil
}

// Create 新建模板。
func (s *ReportTemplateStore) Create(ctx context.Context, t ReportTemplate) (int64, error) {
	sec, _ := json.Marshal(t.Sections)
	var id int64
	err := s.db.QueryRowContext(ctx, `
INSERT INTO report_templates(name, is_default, page_header, classification, customer_override, sections)
VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		t.Name, t.IsDefault, t.PageHeader, t.Classification, t.CustomerOverride, sec).Scan(&id)
	return id, err
}

// Update 更新模板（is_default 由 SetDefault 单独管，避免多默认并发写入）。
func (s *ReportTemplateStore) Update(ctx context.Context, t ReportTemplate) error {
	sec, _ := json.Marshal(t.Sections)
	_, err := s.db.ExecContext(ctx, `
UPDATE report_templates SET name=$1, page_header=$2, classification=$3,
       customer_override=$4, sections=$5, updated_at=now() WHERE id=$6`,
		t.Name, t.PageHeader, t.Classification, t.CustomerOverride, sec, t.ID)
	return err
}

// Delete 删除模板。默认模板不允许删除（先设别的为默认）。
func (s *ReportTemplateStore) Delete(ctx context.Context, id int64) error {
	var isDefault bool
	if err := s.db.QueryRowContext(ctx, `SELECT is_default FROM report_templates WHERE id=$1`, id).Scan(&isDefault); err != nil {
		return err
	}
	if isDefault {
		return ErrDefaultTemplateDelete
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM report_templates WHERE id=$1`, id)
	return err
}

// SetDefault 把某模板设为默认（同事务内清掉其它默认）。
func (s *ReportTemplateStore) SetDefault(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE report_templates SET is_default=false, updated_at=now() WHERE is_default`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE report_templates SET is_default=true, updated_at=now() WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// List 返回全部模板（默认在前，其余按 id）。
func (s *ReportTemplateStore) List(ctx context.Context) ([]ReportTemplate, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+reportTemplateCols+` FROM report_templates ORDER BY is_default DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReportTemplate{}
	for rows.Next() {
		t, err := scanReportTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// Default 返回默认模板；没有配置时返回 nil, nil（调用方沿用内置默认，行为不变）。
func (s *ReportTemplateStore) Default(ctx context.Context) (*ReportTemplate, error) {
	t, err := scanReportTemplate(s.db.QueryRowContext(ctx,
		`SELECT `+reportTemplateCols+` FROM report_templates WHERE is_default LIMIT 1`))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}
