package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/just805/pangu/db"
)

// ═══ M-04：三态收口（confirmed / ruled_out / open_proof_gap）═══
//
// 背景：现有的机械证据闸门（validateMechanicalEvidence）管的是「没证据不许说高危」，
// 属于**定级上限**；但「已排除」一直只是模型自由发挥的一句话，无法回答「哪个控制点
// 拦住的、卡在攻击路径哪一步、由哪次请求证明」。否定结论因此既不可核验，也进不了
// 报告的「覆盖与排除」章节。三态把否定结论同样变成结构化、可核验的数据。
//
// 与既有机制的分工：
//   - validateFindingEvidence / validateMechanicalEvidence：管上限（证据不足→打回/降级）；
//   - severityFloorNote：管下限（有实锤→建议别自谦成 low，只提示不阻断）；
//   - 本文件的三态：管**否定结论**的收口口径。
const (
	// FactConfirmed 有可核验证据支撑的结论。默认值，兼容既有无此字段的历史事实。
	FactConfirmed = "confirmed"
	// FactRuledOut 已排除。必须指名控制点，否则「已排除」只是模型自述。
	FactRuledOut = "ruled_out"
	// FactOpenProofGap 检查做了但证据不足以定论（未验证），不许静默算作通过。
	FactOpenProofGap = "open_proof_gap"
)

// normalizeFactVerdict 归一三态取值，并接受中文写法（模型经常写中文）。
// 空值按 confirmed 处理——历史调用方没有这个字段。无法识别时返回空串，
// 由调用方报错而不是静默当成 confirmed（静默兜底会让写错枚举的结论被误当确认）。
func normalizeFactVerdict(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "confirmed", "confirm", "确认", "已确认":
		return FactConfirmed
	case "ruled_out", "ruledout", "ruled-out", "排除", "已排除":
		return FactRuledOut
	case "open_proof_gap", "open-proof-gap", "openproofgap", "unverified", "未验证", "待验证":
		return FactOpenProofGap
	}
	return ""
}

// validateFactVerdict 是三态收口的确定性校验。返回空串表示通过，否则是回灌给模型的
// 中文纠正提示（与 validateMechanicalEvidence 同一约定，模型可在同会话内修正重试）。
//
// 规则：
//  1. ruled_out 必须给 control_point —— 「哪个防护、卡在攻击路径哪一步、哪次请求证明」。
//     缺了它就把「排除」降级成 open_proof_gap，而不是让一句空话进图谱。
//  2. ruled_out / open_proof_gap 必须有 evidence —— 否则等于「我觉得没有」。
//  3. open_proof_gap 不允许标 observed —— 没有定论就不可能是「输出里直接看到」。
//     confidence 为空时不校验（老调用方不传）。
func validateFactVerdict(verdict, controlPoint, evidence, confidence string) string {
	switch verdict {
	case FactRuledOut:
		if strings.TrimSpace(controlPoint) == "" {
			return "「已排除」必须指名控制点：用 control_point 写清【哪个防护（WAF/鉴权/参数化查询/白名单等）】+" +
				"【卡在攻击路径哪一步】+【哪次请求或哪条输出证明】。" +
				"如果实际只是探了一次没打进去、控制点说不清楚，请改判 verdict=open_proof_gap（未验证），" +
				"并说明还差什么证据——「未验证」比假的「已排除」对后续规划有用得多。"
		}
		if strings.TrimSpace(evidence) == "" {
			return "「已排除」必须给 evidence（那一行关键证据：被拦的请求/响应状态码或拒绝页特征）。" +
				"没有证据的排除请改判 verdict=open_proof_gap。"
		}
	case FactOpenProofGap:
		if strings.TrimSpace(evidence) == "" {
			return "「未验证（open_proof_gap）」也要给 evidence：至少写清你实际做了什么探测、看到了什么。" +
				"什么都没做就留空，请不要写这条事实。"
		}
		if strings.EqualFold(strings.TrimSpace(confidence), "observed") {
			return "「未验证（open_proof_gap）」不能标 confidence=observed：observed 表示结论已被输出直接证实，" +
				"而 open_proof_gap 表示证据不足以定论。若确实直接看到了拦截/拒绝，请改判 verdict=ruled_out 并填 control_point；" +
				"否则把 confidence 改为 inferred。"
		}
	}
	return ""
}

// verdictOfNode 读取 fact 节点 payload 里的三态结论。非 fact 节点、payload 解析失败
// 或没有该字段，一律按 confirmed 处理（历史数据没有 verdict，语义上就是「正常结论」）。
func verdictOfNode(n *db.Node) string {
	if n == nil || n.Kind != db.KindFact || len(n.Payload) == 0 {
		return FactConfirmed
	}
	var p struct {
		Verdict string `json:"verdict"`
	}
	if err := json.Unmarshal(n.Payload, &p); err != nil {
		return FactConfirmed
	}
	if v := normalizeFactVerdict(p.Verdict); v != "" {
		return v
	}
	return FactConfirmed
}

// severityFloorNote 是定级下限的**建议性**校验（只提示、不阻断）。
//
// 上限闸门管「没证据不许说高危」；下限管相反的失真：evidence 里已经带了可机械复现的
// 验证凭据，severity 却给了 low——审核时会被当成「AI 把真漏洞写小了」。是否真的低危
// 只有模型知道（信息泄露也可能确实无影响），所以这里返回提示而不是拒绝。
func severityFloorNote(severity, evidence string) string {
	if strings.ToLower(strings.TrimSpace(severity)) != "low" {
		return ""
	}
	if !hasMechanicalVerification(evidence) {
		return ""
	}
	return fmt.Sprintf("定级复核（不阻断）：evidence 里已包含可机械复现的验证凭据，但 severity 给的是 low。" +
		"若该问题能造成越权访问、数据读取或命令执行，实际应为 medium 及以上，请复核后重报；" +
		"若确认影响轻微（例如仅泄露无敏感信息的版本号），请在 summary 里写明「影响轻微」的判断依据。")
}
