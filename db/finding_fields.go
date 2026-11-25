package db

import (
	"context"
	"encoding/json"
)

// ═══ M-11：漏洞字段 schema 化 ═══
//
// 内置字段由代码种子写入（ON CONFLICT DO NOTHING）：不覆盖用户改过的 label/hint，
// 也不会复活用户删除的自定义字段（只种 builtin 行）。
//
// 为什么需要它：报告模板要按字段 id 绑定渲染（M-10）、模型要按 schema 填报、
// CVSS 要由平台算而不是模型自评——三者都要求「字段」先是一个可枚举的对象。

// FindingFieldDef 是一条字段定义。
type FindingFieldDef struct {
	Key        string   `json:"key"`
	Label      string   `json:"label"`
	Type       string   `json:"type"`
	EnumValues []string `json:"enum_values,omitempty"`
	Required   bool     `json:"required"`
	Builtin    bool     `json:"builtin"`
	Hint       string   `json:"hint,omitempty"`
	SortOrder  int      `json:"sort_order"`
}

// builtinFindingFields 是内置字段表。顺序即展示顺序（sort_order 递增）。
var builtinFindingFields = []FindingFieldDef{
	{Key: "vulnclass", Label: "漏洞类别", Type: "enum", Required: true, SortOrder: 10,
		Hint: "标准类别枚举（见 VulnClassPromptText）；具体 CVE 编号写进 name"},
	{Key: "name", Label: "漏洞名称", Type: "text", Required: true, SortOrder: 20,
		Hint: "可读标题；已知 CVE 写编号"},
	{Key: "severity", Label: "严重度", Type: "enum", Required: true, SortOrder: 30,
		EnumValues: []string{"critical", "high", "medium", "low"},
		Hint:       "critical/high 必须有可机械复现的证据"},
	{Key: "cvss_vector", Label: "CVSS 向量", Type: "text", SortOrder: 40,
		Hint: "CVSS v3.1 向量串，如 CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H；由平台自动算分"},
	{Key: "cvss_score", Label: "CVSS 评分", Type: "number", SortOrder: 50,
		Hint: "由 cvss_vector 自动计算，不可手填"},
	{Key: "cwe", Label: "CWE", Type: "text", SortOrder: 60,
		Hint: "如 CWE-89；可选"},
	{Key: "attack_technique", Label: "ATT&CK 技术", Type: "text", SortOrder: 70,
		Hint: "如 T1190；可选"},
	{Key: "summary", Label: "摘要", Type: "multiline", SortOrder: 80,
		Hint: "一句话说清影响面"},
	{Key: "evidence", Label: "证据 / PoC", Type: "multiline", SortOrder: 90,
		Hint: "命令 + 输出；critical/high 必须可机械复现"},
	{Key: "remediation", Label: "修复建议", Type: "multiline", SortOrder: 100,
		Hint: "可选；报告修复路线图会引用"},
}

// SeedFindingFieldDefs 幂等地写入内置字段定义（不覆盖已有行）。
func (d *DB) SeedFindingFieldDefs(ctx context.Context) error {
	for _, f := range builtinFindingFields {
		enums, _ := json.Marshal(f.EnumValues)
		if _, err := d.ExecContext(ctx, `
INSERT INTO finding_field_defs(key, label, type, enum_values, required, builtin, hint, sort_order)
VALUES ($1,$2,$3,$4,$5,true,$6,$7)
ON CONFLICT (key) DO NOTHING`,
			f.Key, f.Label, f.Type, enums, f.Required, f.Hint, f.SortOrder); err != nil {
			return err
		}
	}
	return nil
}

// ListFindingFieldDefs 返回全部字段定义（按 sort_order）。
func (d *DB) ListFindingFieldDefs(ctx context.Context) ([]FindingFieldDef, error) {
	rows, err := d.QueryContext(ctx, `
SELECT key, label, type, enum_values, required, builtin, hint, sort_order
FROM finding_field_defs ORDER BY sort_order, key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FindingFieldDef{}
	for rows.Next() {
		var f FindingFieldDef
		var enums []byte
		if err := rows.Scan(&f.Key, &f.Label, &f.Type, &enums, &f.Required, &f.Builtin, &f.Hint, &f.SortOrder); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(enums, &f.EnumValues)
		out = append(out, f)
	}
	return out, rows.Err()
}

// FindingFieldDefs 返回漏洞字段定义（M-11）。字段定义是全局的、不属于某张探索图，
// 但工具侧只持有 t.ts，这里用探索图句柄委托，省得再注入一个 store。
func (s *ExplorationStore) FindingFieldDefs(ctx context.Context) ([]FindingFieldDef, error) {
	return s.db.ListFindingFieldDefs(ctx)
}
