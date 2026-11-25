package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	actool "github.com/Autumn-27/norma/tool"

	"github.com/just805/pangu/db"
)

// ═══ M-09：合规基线核查工具 ═══
//
// 两个工具：查清单（把基线项 + 本任务已有结果合在一起，缺口一眼可见）、
// 记录结果（pass/fail/na + 证据）。
// 不符合项不在这里自动转 finding——让模型显式调用 report_finding，
// 这样证据闸门、去重、报告聚合全部照常生效。

const complianceEvidenceHint = "「不符合」必须给 evidence（看到什么才算不符合）；没有证据的项请记 unknown，别猜。"

// normalizeComplianceStatus 归一核查结论。
func normalizeComplianceStatus(v string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "pass", "符合", "通过":
		return "pass", true
	case "fail", "不符合", "未通过", "不通过":
		return "fail", true
	case "na", "n/a", "不适用":
		return "na", true
	case "", "unknown", "未知", "未测":
		return "unknown", true
	}
	return "", false
}

func (t *ToolSet) complianceChecklist() actool.CoreTool {
	return t.readExpTool("compliance_checklist",
		"查看【合规基线核查清单】：基线项（等保二级 djbh2 / CIS Web cis-web）+ 本任务已记录的结果 + 缺口统计。\n"+
			"用途：给甲方做合规交付时，先看清单决定查哪些项；查完用 compliance_record 回填。"+
			"机器能自动判的项（TLS/响应头/目录列表等）用既有工具实测，其余给出人工核查依据。",
		obj(map[string]any{
			"baseline": str("基线名：djbh2（等保 2.0 二级）| cis-web（CIS Web 精简）"),
		}, "baseline"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Baseline string `json:"baseline"`
			}
			_ = json.Unmarshal(in, &a)
			baseline := strings.TrimSpace(a.Baseline)
			items, ok := db.ComplianceBaseline(baseline)
			if !ok {
				return actool.Errorf(fmt.Sprintf("未知基线 %q：可用 %s", baseline, strings.Join(db.ComplianceBaselines(), " / "))), nil
			}
			results, err := t.ts.Compliance().ListResults(ctx, t.taskID, baseline)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			byKey := make(map[string]db.ComplianceResult, len(results))
			for _, r := range results {
				byKey[r.ItemKey] = r
			}
			out := make([]map[string]any, 0, len(items))
			stats := map[string]int{"total": len(items)}
			for _, it := range items {
				m := map[string]any{"key": it.Key, "title": it.Title, "method": it.Method, "expect": it.Expect}
				if r, has := byKey[it.Key]; has {
					m["status"] = r.Status
					m["evidence"] = r.Evidence
					m["note"] = r.Note
					stats[r.Status]++
				} else {
					m["status"] = "unknown"
					stats["unknown"]++
				}
				out = append(out, m)
			}
			res := map[string]any{"baseline": baseline, "items": out, "stats": stats}
			if stats["unknown"] > 0 {
				res["hint"] = fmt.Sprintf("还有 %d 项未核查；不符合项确认后用 report_finding 上报（类别选「其他」或对应类别），再 compliance_record 记 fail。", stats["unknown"])
			}
			return jsonResult(res)
		})
}

func (t *ToolSet) complianceRecord() actool.CoreTool {
	return t.writeExpTool("compliance_record",
		"记录【合规基线】核查结果：pass（符合）/ fail（不符合）/ na（不适用）/ unknown。\n"+
			"同一 基线+项 重复提交会覆盖更新。"+complianceEvidenceHint,
		obj(map[string]any{
			"baseline": str("基线名：djbh2 | cis-web"),
			"entries": map[string]any{
				"type":        "array",
				"description": "核查结果数组（1-50 条）",
				"items": obj(map[string]any{
					"item_key": str("基线项 key（用 compliance_checklist 查到的 key）"),
					"status":   str("pass | fail | na | unknown"),
					"evidence": str("支撑结论的证据（不符合时必填）"),
					"note":     str("备注（可选）"),
				}, "item_key", "status"),
			},
		}, "baseline", "entries"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Baseline string `json:"baseline"`
				Entries  []struct {
					ItemKey  string `json:"item_key"`
					Status   string `json:"status"`
					Evidence string `json:"evidence"`
					Note     string `json:"note"`
				} `json:"entries"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			baseline := strings.TrimSpace(a.Baseline)
			items, ok := db.ComplianceBaseline(baseline)
			if !ok {
				return actool.Errorf(fmt.Sprintf("未知基线 %q：可用 %s", baseline, strings.Join(db.ComplianceBaselines(), " / "))), nil
			}
			valid := make(map[string]bool, len(items))
			for _, it := range items {
				valid[it.Key] = true
			}
			if len(a.Entries) == 0 {
				return actool.Errorf("entries 不能为空"), nil
			}
			if len(a.Entries) > 50 {
				a.Entries = a.Entries[:50]
			}
			saved := 0
			errs := map[string]string{}
			for i, e := range a.Entries {
				key := strings.TrimSpace(e.ItemKey)
				if !valid[key] {
					errs[fmt.Sprint(i)] = fmt.Sprintf("item_key %q 不属于基线 %s（用 compliance_checklist 查 key）", key, baseline)
					continue
				}
				st, ok := normalizeComplianceStatus(e.Status)
				if !ok {
					errs[fmt.Sprint(i)] = fmt.Sprintf("status 只能是 pass|fail|na|unknown：%q", e.Status)
					continue
				}
				if st == "fail" && strings.TrimSpace(e.Evidence) == "" {
					errs[fmt.Sprint(i)] = "「不符合」必须给 evidence：写清实际看到什么（响应头/配置/回显），否则无法在交付件里复核"
					continue
				}
				if err := t.ts.Compliance().UpsertResult(ctx, t.taskID, baseline, db.ComplianceResult{
					ItemKey: key, Status: st,
					Evidence: strings.TrimSpace(e.Evidence), Note: strings.TrimSpace(e.Note),
				}); err != nil {
					errs[fmt.Sprint(i)] = err.Error()
					continue
				}
				saved++
			}
			out := map[string]any{"saved": saved}
			if len(errs) > 0 {
				out["errors"] = errs
			}
			return jsonResult(out)
		})
}
