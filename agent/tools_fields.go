package agent

import (
	"context"
	"encoding/json"

	actool "github.com/Autumn-27/norma/tool"
)

// ═══ M-11：漏洞字段定义查询工具 ═══
//
// 让模型按 schema 填报，而不是靠提示词记住有哪些字段。字段定义可被 UI 调整
// （label/hint），但 key 与类型固定——report_finding 的入参与报告模板都锚在 key 上。
func (t *ToolSet) findingFields() actool.CoreTool {
	return t.readExpTool("finding_fields",
		"查看【漏洞字段定义】：字段名、类型、枚举取值、是否必填、填报说明。\n"+
			"上报漏洞前可先看它确认字段口径（尤其 cvss_vector：给完整 v3.1 向量，平台自动算分）。",
		obj(map[string]any{}),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			defs, err := t.ts.FindingFieldDefs(ctx)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return jsonResult(map[string]any{
				"fields": defs,
				"note":   "cvss_score 由 cvss_vector 自动计算，不要手填；severity 与 CVSS 档位不一致时平台会在回执里提示复核。",
			})
		})
}
