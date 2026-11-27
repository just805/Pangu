package report

// A17 页面报告与导出同源：GET /api/report 的 Markdown 从 findings 表出（含
// reporter 正文/复测/流量证据/情报富化/执行摘要），与 Word 导出共用同一份
// TaskDocxInput 数据组装——前端预览与交付文档是同一份报告的两个视图。
//
// 章节结构与 BuildTaskDocx 一一对应：头部 → 执行摘要 → 一、评估概览 →
// 二、范围与方法论 → 三、漏洞明细 → 四、修复路线图 → 附录 A。
// 旧渲染器 report.Markdown（探索节点取数）保留给非 findings 场景（如无库任务）。

import (
	"fmt"
	"sort"
	"strings"

	"github.com/just805/pangu/db"
)

// TaskMarkdown 渲染任务报告 Markdown（与 BuildTaskDocx 同源同结构）。
func TaskMarkdown(in TaskDocxInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", nz(in.Title, "渗透测试报告"))

	meta := []string{}
	if in.Customer != "" {
		meta = append(meta, fmt.Sprintf("**委托方**：%s", in.Customer))
	}
	meta = append(meta, "**密级**："+nz(in.Classification, "内部资料 · 仅限授权方"))
	meta = append(meta, fmt.Sprintf("**报告生成时间**：%s", FormatTimeUTC(in.GeneratedAt)))
	meta = append(meta, fmt.Sprintf("**漏洞总数**：%d（严重 %d / 高危 %d / 中危 %d / 低危 %d）",
		len(in.Findings), countSev(in.Findings, "critical"), countSev(in.Findings, "high"),
		countSev(in.Findings, "medium"), countSev(in.Findings, "low")))
	if in.TestWindow != "" {
		meta = append(meta, "**测试窗口**："+in.TestWindow)
	}
	for _, m := range meta {
		fmt.Fprintf(&b, "- %s\n", m)
	}
	b.WriteString("\n")

	// ── 执行摘要（A13）──
	if s := strings.TrimSpace(in.ExecSummary); s != "" {
		b.WriteString("## 执行摘要\n\n")
		b.WriteString(s)
		b.WriteString("\n\n")
	}

	// ── 一、评估概览 ──
	b.WriteString("## 一、评估概览\n\n")
	if len(in.AssetCounts) > 0 {
		b.WriteString("### 资产范围\n\n")
		b.WriteString("| 资产类型 | 数量 |\n|---|---|\n")
		types := make([]string, 0, len(in.AssetCounts))
		for k := range in.AssetCounts {
			types = append(types, k)
		}
		sort.Strings(types)
		total := 0
		for _, ty := range types {
			fmt.Fprintf(&b, "| %s | %d |\n", assetTypeCN(ty), in.AssetCounts[ty])
			total += in.AssetCounts[ty]
		}
		fmt.Fprintf(&b, "| 合计 | %d |\n\n", total)
	}
	b.WriteString("### 漏洞严重度分布\n\n")
	b.WriteString("| 严重度 | 数量 |\n|---|---|\n")
	for _, sev := range []string{"critical", "high", "medium", "low"} {
		if n := countSev(in.Findings, sev); n > 0 {
			fmt.Fprintf(&b, "| %s | %d |\n", SeverityCN(sev), n)
		}
	}
	b.WriteString("\n### 漏洞类别分布\n\n")
	if groups := groupByClass(in.Findings); len(groups) > 0 {
		b.WriteString("| 漏洞类别 | 条数 | 涉及资产数 |\n|---|---|---|\n")
		for _, g := range groups {
			fmt.Fprintf(&b, "| %s | %d | %d |\n", g.class, len(g.items), len(g.assets))
		}
		b.WriteString("\n")
	} else {
		b.WriteString("_本次未确认漏洞。_\n\n")
	}

	// ── 二、测试范围与方法论 ──
	b.WriteString("## 二、测试范围与方法论\n\n")
	if in.Goal != "" {
		fmt.Fprintf(&b, "### 任务目标\n\n%s\n\n", in.Goal)
	}
	if len(in.Goals) > 0 {
		met, total := GoalTally(in.Goals)
		fmt.Fprintf(&b, "### 目标达成情况\n\n")
		if met < total {
			fmt.Fprintf(&b, "共 %d 个任务目标，已证明达成 %d 个；未达成目标如实记录，本次任务按「未完成」归档。\n\n", total, met)
		} else {
			fmt.Fprintf(&b, "共 %d 个任务目标，全部已由可复现证据证明达成。\n\n", total)
		}
		b.WriteString("| 目标 | 状态 | 验收标准 |\n|---|---|---|\n")
		for _, g := range in.Goals {
			state := "未达成"
			if g.Met() {
				state = "已达成"
			}
			fmt.Fprintf(&b, "| %s | %s | %s |\n", mdCell(g.Text), state, mdCell(nz(g.Criteria, "—")))
		}
		b.WriteString("\n")
	}
	if in.TestWindow != "" {
		fmt.Fprintf(&b, "### 测试窗口\n\n%s\n\n", in.TestWindow)
	}
	if len(in.AssetCounts) > 0 {
		b.WriteString("### 测试范围\n\n")
		types := make([]string, 0, len(in.AssetCounts))
		for k := range in.AssetCounts {
			types = append(types, k)
		}
		sort.Strings(types)
		parts := make([]string, 0, len(types))
		for _, ty := range types {
			parts = append(parts, fmt.Sprintf("%s ×%d", assetTypeCN(ty), in.AssetCounts[ty]))
		}
		fmt.Fprintf(&b, "本次测试覆盖：%s（明细见附录 A）。\n\n", strings.Join(parts, "；"))
	}
	b.WriteString("### 方法论\n\n本次评估由 AI 多 Agent 自主渗透平台 PANGU 执行：规划 Agent 依据目标与资产态势派发探测意图，执行 Agent 以真实工具（nmap/httpx/nuclei/ffuf/sqlmap 等）完成侦察、漏洞验证与利用，全程流量经记录代理留痕；漏洞结论均要求可复现证据（PoC 命令/请求响应）支撑，critical/high 级发现建议按复测记录进行独立复验。严重度评定参照 CVSS v3.1 结合业务影响人工校准。\n\n")
	// ── 覆盖与排除（与 Word 同源；M-04 起消费三态结论）──
	if cov := in.Coverage; cov != nil && ((cov.ScopeRows > 0 && cov.Denominator > 0) || len(cov.Excluded) > 0 || cov.OpenProofGaps > 0) {
		b.WriteString("### 覆盖与排除\n\n")
		if cov.ScopeRows > 0 && cov.Denominator > 0 {
			pct := "—"
			if cov.Pct != nil {
				pct = fmt.Sprintf("%.0f%%", *cov.Pct*100)
			}
			fmt.Fprintf(&b, "任务范围共 %d 条资产，已触达约 %d 条（覆盖率 %s）。未触达不代表没有漏洞，只表示本次未在该资产上形成结论。\n\n",
				cov.Denominator, cov.Tested, pct)
		}
		if len(cov.Excluded) > 0 {
			fmt.Fprintf(&b, "明确排除了 %d 个方向，均附拦截控制点与证据：\n\n", len(cov.Excluded))
			b.WriteString("| 已排除方向 | 拦截控制点 |\n|---|---|\n")
			for _, e := range cov.Excluded {
				fmt.Fprintf(&b, "| %s | %s |\n", mdCell(clipRunes(e.Summary, 80)), mdCell(nz(e.ControlPoint, "—")))
			}
			b.WriteString("\n")
		}
		if cov.OpenProofGaps > 0 {
			fmt.Fprintf(&b, "另有 %d 个方向做了探测但证据不足以定论，记为「未验证」——未验证不等于无风险。\n\n", cov.OpenProofGaps)
		}
	}

	// ── 三、漏洞明细 ──
	b.WriteString("## 三、漏洞明细\n\n")
	groups := groupByClass(in.Findings)
	for gi, g := range groups {
		fmt.Fprintf(&b, "### 3.%d %s（共 %d 条）\n\n", gi+1, g.class, len(g.items))
		for fi, f := range g.items {
			fmt.Fprintf(&b, "#### 3.%d.%d [%s] %s\n\n", gi+1, fi+1, SeverityCN(f.Severity), findingTitle(f))
			fmt.Fprintf(&b, "- **漏洞类别**：%s\n", nz(f.VulnClass, "未分类"))
			// CVSS（M-11）：与 Word 同源，向量由 Agent 提供、分数由平台计算。
			if f.CVSSScore != nil {
				fmt.Fprintf(&b, "- **CVSS**：%.1f  %s\n", *f.CVSSScore, nz(f.CVSSVector, ""))
			}
			if len(f.AssetIDs) > 0 {
				fmt.Fprintf(&b, "- **影响资产**：%s\n", assetNames(f.AssetIDs, in.AssetNames))
			}
			fmt.Fprintf(&b, "- **发现时间**：%s · **发现者**：%s\n", FormatTimeUTC(f.CreatedAt), nz(f.Worker, "-"))
			b.WriteString("\n")
			if s := strings.TrimSpace(f.Summary); s != "" {
				fmt.Fprintf(&b, "**摘要**：%s\n\n", s)
			}
			if md := enrichmentMarkdown(in, f); md != "" {
				b.WriteString(md)
			}
			if body := strings.TrimSpace(f.Report); body != "" {
				fmt.Fprintf(&b, "**详细报告**\n\n%s\n\n", body)
			}
			if ev := strings.TrimSpace(f.Evidence); ev != "" {
				fmt.Fprintf(&b, "**证据 / PoC**\n\n```\n%s\n```\n\n", ev)
			}
			b.WriteString(findingTrafficMarkdown(f, false))
		}
	}
	if len(groups) == 0 {
		b.WriteString("_本次未确认漏洞。_\n\n")
	}

	// ── 四、攻击路径（C2：与 Word 同源）──
	writeAttackChainMarkdown(&b, in.AttackChain, in.AssetNames)

	// ── 五、客户端/二进制分析（P0）──
	writeReverseChapterMarkdown(&b, in.ReverseSamples, in.ReverseSecrets)

	// ── 六、修复优先级路线图 ──
	b.WriteString("## 六、修复优先级路线图\n\n")
	tiers := []struct {
		title  string
		sevs   []string
		advice string
	}{
		{"立即整改（建议 7 天内）", []string{"critical", "high"},
			"存在被直接利用/造成数据泄露风险，建议立即安排修复并临时缓解（WAF 规则/下线/访问控制收紧）。",
		},
		{"近期计划（建议 30 天内）", []string{"medium"},
			"需特定条件利用或影响有限，纳入常规迭代修复，修复后走复测验证。",
		},
		{"择机安排", []string{"low", "info"},
			"风险较低或为加固建议，随版本迭代处理；涉及配置类项目可先行调整。",
		},
	}
	for _, tier := range tiers {
		var items []*db.DBFinding
		for _, f := range in.Findings {
			for _, s := range tier.sevs {
				if strings.EqualFold(f.Severity, s) {
					items = append(items, f)
					break
				}
			}
		}
		fmt.Fprintf(&b, "### %s\n\n", tier.title)
		if len(items) == 0 {
			b.WriteString("本档暂无。\n\n")
			continue
		}
		fmt.Fprintf(&b, "%s\n\n", tier.advice)
		sort.SliceStable(items, func(i, j int) bool { return sevRankKey(items[i]) < sevRankKey(items[j]) })
		b.WriteString("| # | 漏洞 | 严重度 | 当前状态 |\n|---|---|---|---|\n")
		for i, f := range items {
			fmt.Fprintf(&b, "| %d | %s | %s | %s |\n", i+1, mdCell(findingTitle(f)), SeverityCN(f.Severity), statusCN(f.Status))
		}
		b.WriteString("\n")
	}

	// ── 七、合规基线核查（M-09 收尾）：与 Word 章同源，有记录才出章 ──
	if len(in.Compliance) > 0 {
		b.WriteString("## 七、合规基线核查\n\n")
		b.WriteString("本章按内置基线逐项列出核查结论。「不符合」项已在漏洞明细中作为发现上报；「未核查」表示本轮未实际检查，不等同于符合。\n\n")
		for _, g := range in.Compliance {
			pass, fail, na, unknown := complianceCounts(g.Lines)
			fmt.Fprintf(&b, "### %s（已记录 %d / 基线共 %d 项）\n\n", g.Label, len(g.Lines), g.Total)
			fmt.Fprintf(&b, "符合 %d 项 · 不符合 %d 项 · 不适用 %d 项 · 未核查 %d 项\n\n", pass, fail, na, unknown)
			b.WriteString("| 检查项 | 结论 | 证据 / 说明 |\n|---|---|---|\n")
			for _, l := range g.Lines {
				fmt.Fprintf(&b, "| %s | %s | %s |\n",
					mdCell(l.Title), complianceStatusCN(l.Status), mdCell(clipRunes(complianceDetail(l), 300)))
			}
			b.WriteString("\n")
		}
	}

	// ── 八、静态扫描线索（M-02）：与 Word 章同源，有记录才出章 ──
	if len(in.CodeScans) > 0 {
		b.WriteString("## 八、静态扫描线索\n\n")
		b.WriteString("本章列出授权范围内源码的静态扫描结果，作为「值得优先动态验证的位置」。" +
			"这些是线索不是结论：未经动态验证的静态命中不计入漏洞统计。\n\n")
		for _, cs := range in.CodeScans {
			title := cs.AssetName
			if cs.Location != "" {
				title += "（" + cs.Location + "）"
			}
			fmt.Fprintf(&b, "### %s · 共 %d 条线索\n\n", title, cs.Total)
			fmt.Fprintf(&b, "- 扫描器：%s\n- 严重度：%s\n\n", distText(cs.ByScanner), distText(cs.BySeverity))
			if len(cs.TopSinks) == 0 {
				continue
			}
			b.WriteString("| 位置 | 规则 / 组件 | 等级 |\n|---|---|---|\n")
			for _, s := range cs.TopSinks {
				loc := s.FilePath
				if s.LineStart != nil && *s.LineStart > 0 {
					loc += fmt.Sprintf(":%d", *s.LineStart)
				}
				rule := s.RuleID
				if rule == "" && s.PackageName != "" {
					rule = s.PackageName + " " + s.PackageVersion
				}
				fmt.Fprintf(&b, "| %s | %s | %s |\n",
					mdCell(loc), mdCell(clipRunes(rule, 80)), strings.ToUpper(s.Severity))
			}
			b.WriteString("\n")
		}
	}

	// ── 附录 A：资产清单 ──
	if len(in.AppendixAssets) > 0 {
		b.WriteString("## 附录 A 资产清单\n\n")
		types := make([]string, 0, len(in.AppendixAssets))
		for k := range in.AppendixAssets {
			types = append(types, k)
		}
		sort.Strings(types)
		const appendixCap = 200
		for _, ty := range types {
			names := in.AppendixAssets[ty]
			if len(names) == 0 {
				continue
			}
			fmt.Fprintf(&b, "### %s（共 %d）\n\n", assetTypeCN(ty), len(names))
			shown := names
			if len(shown) > appendixCap {
				shown = shown[:appendixCap]
			}
			b.WriteString("| # | 资产 |\n|---|---|\n")
			for i, n := range shown {
				fmt.Fprintf(&b, "| %d | %s |\n", i+1, mdCell(n))
			}
			if len(names) > appendixCap {
				fmt.Fprintf(&b, "\n（其余 %d 条略，可在平台「资产」页查看全部。）\n", len(names)-appendixCap)
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

// mdCell 表格单元格转义：竖线会破表。
func mdCell(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}
