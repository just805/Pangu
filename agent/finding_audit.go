package agent

// 漏洞证据的确定性复核（上游 0.4.102 P0-5 的轻量落地）。
//
// 目的：报告里最贵的是误报。这里不再引入一个 LLM 审核角色，而是按「验证策略」
// 做机械检查——有没有 baseline+proof 两条流量、有没有双身份对照、有没有可重放
// 命令与输出、影响证据是否够具体。检查结果随 report_finding 回执返回，模型能
// 立刻看到缺什么并补齐；不阻断入库，critical/high 的缺口会在 evidence_note 里
// 明确列出，供规划者/报告阶段复核。

import (
	"regexp"
	"strings"

	"github.com/just805/pangu/db"
)

// FindingAudit 是一次确定性复核的结果。
type FindingAudit struct {
	Verdict  string   `json:"verdict"`  // pass | fail | needs_human
	Strategy string   `json:"strategy"` // http_diff | dual_identity | effect_proof | poc_replay | oracle | manual
	Missing  []string `json:"missing,omitempty"`
}

var (
	replayCmdRe   = regexp.MustCompile(`(?i)(curl|sqlmap|nmap|ffuf|nuclei|hydra|wget|Invoke-WebRequest)\s`)
	httpOutputRe  = regexp.MustCompile(`(?i)(HTTP/1\.[01]\s+\d{3}|\b\d{3}\s+(OK|Forbidden|Unauthorized)|\buid=0\b|\broot\b)`)
	identityHitRe = regexp.MustCompile(`(?i)(身份\s*[AB12]|identity\s*[AB12]|用户\s*A|用户\s*B|cookie\s*[AB]\s*[:=]|authorization\s*[:=])`)
	effectHitRe   = regexp.MustCompile(`(身份证|手机号|住址|姓名|密码|诊疗|患者|uid=0|root|/etc/passwd|银行卡|邮箱)`)
	controlHitRe  = regexp.MustCompile(`(?i)(对照|baseline|正常请求|未登录|匿名|admin\s*vs|A/B)`)
)

// AuditFindingEvidence 按证据形态推断验证策略并做机械检查。
// severity 为 low/medium 时不判 fail（证据要求本就低），只给 needs_human。
func AuditFindingEvidence(severity, evidence string, refs []db.TrafficRef) FindingAudit {
	hasBaseline, hasProof := false, false
	for _, r := range refs {
		switch strings.ToLower(strings.TrimSpace(r.Role)) {
		case "baseline":
			hasBaseline = true
		case "proof":
			hasProof = true
		}
	}
	ev := strings.TrimSpace(evidence)
	// 只要绑了流量就按 http_diff 验：缺 baseline/proof 会由下面的检查点名，
	// 而不是退回 manual（否则「只绑了 proof」会被当成无需补充）。
	strategy := inferVerificationStrategy(ev, hasBaseline || hasProof)

	audit := FindingAudit{Strategy: strategy, Verdict: "needs_human"}
	switch strategy {
	case "http_diff":
		if hasBaseline && hasProof {
			audit.Verdict = "pass"
		} else {
			audit.Verdict = "fail"
			if !hasBaseline {
				audit.Missing = append(audit.Missing, "缺少 baseline（正常对照）流量")
			}
			if !hasProof {
				audit.Missing = append(audit.Missing, "缺少 proof（漏洞证明）流量")
			}
		}
	case "dual_identity":
		if n := len(identityHitRe.FindAllString(ev, -1)); n >= 2 {
			audit.Verdict = "pass"
		} else {
			audit.Verdict = "fail"
			audit.Missing = append(audit.Missing, "需要两个身份的请求/响应对照（如 A 用户可读、B 用户越权读到）")
		}
	case "poc_replay":
		if replayCmdRe.MatchString(ev) && httpOutputRe.MatchString(ev) {
			audit.Verdict = "pass"
		} else {
			audit.Verdict = "fail"
			if !replayCmdRe.MatchString(ev) {
				audit.Missing = append(audit.Missing, "缺少可重放命令（curl/sqlmap 等）")
			}
			if !httpOutputRe.MatchString(ev) {
				audit.Missing = append(audit.Missing, "缺少命令输出关键行（HTTP 状态码/响应特征/uid=0 等）")
			}
		}
	case "effect_proof":
		if len([]rune(ev)) >= 120 && effectHitRe.MatchString(ev) {
			audit.Verdict = "pass"
		} else {
			audit.Verdict = "fail"
			if len([]rune(ev)) < 120 {
				audit.Missing = append(audit.Missing, "影响证据过短（需 ≥120 字，说明读到了什么、范围多大）")
			}
			if !effectHitRe.MatchString(ev) {
				audit.Missing = append(audit.Missing, "缺少具体影响字段（敏感字段名/命令回显/文件内容）")
			}
		}
	case "oracle":
		if controlHitRe.MatchString(ev) {
			audit.Verdict = "pass"
		} else {
			audit.Verdict = "needs_human"
			audit.Missing = append(audit.Missing, "缺少对照结果（正常/异常两组响应差异）")
		}
	default:
		audit.Verdict = "needs_human"
	}

	// 低危/中危不因机械检查不足判 fail：避免为低价值发现反复打断工作流。
	if audit.Verdict == "fail" {
		switch strings.ToLower(strings.TrimSpace(severity)) {
		case "low", "medium", "info":
			audit.Verdict = "needs_human"
		}
	}
	return audit
}

// inferVerificationStrategy 按证据形态推断策略。hasTraffic 表示至少绑定了一条
// 流量（缺 baseline/proof 由校验阶段点名）。
func inferVerificationStrategy(evidence string, hasTraffic bool) string {
	if hasTraffic {
		return "http_diff"
	}
	if len(identityHitRe.FindAllString(evidence, -1)) >= 2 {
		return "dual_identity"
	}
	if replayCmdRe.MatchString(evidence) {
		return "poc_replay"
	}
	if len([]rune(evidence)) >= 120 && effectHitRe.MatchString(evidence) {
		return "effect_proof"
	}
	if controlHitRe.MatchString(evidence) {
		return "oracle"
	}
	return "manual"
}

// auditNote 把复核结果压成一句可读提示（fail 才展开 missing）。
func auditNote(a FindingAudit) string {
	if a.Verdict == "pass" {
		return ""
	}
	if len(a.Missing) == 0 {
		return ""
	}
	return "证据复核（" + a.Strategy + "）待补：" + strings.Join(a.Missing, "；")
}
