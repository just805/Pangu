package report

// A11 漏洞情报富化：「已知漏洞」类 finding 在报告层自动带 CVSS 评分/向量/KEV
// 在野利用标记与官方描述。情报库（vuln_cves）与资产联动已久，报告层此前零消费；
// 这里保持 report 包与 vulnintel 解耦——server 组装时查情报库，映射成本包的
// CVEIntel，渲染层只认本包类型。

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/just805/pangu/db"
)

// cveRe 从 finding 的 name/vulnclass/summary 里提取 CVE 编号（大小写不敏感，
// 统一转大写去重）。
var cveRe = regexp.MustCompile(`(?i)CVE-\d{4}-\d{4,}`)

// CVEIntel 是报告渲染所需的单条漏洞情报快照（server 从 vulnintel.VulnDetail 映射）。
type CVEIntel struct {
	CVEID       string   `json:"cve_id"`
	Severity    string   `json:"severity"` // NVD 原始口径 CRITICAL/HIGH/...
	Score       *float64 `json:"cvss_score,omitempty"`
	Vector      string   `json:"cvss_vector,omitempty"`
	KEV         bool     `json:"kev"`
	Published   string   `json:"published,omitempty"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Refs        int      `json:"refs,omitempty"` // 参考链接数（报告里只给数量）
}

// ExtractCVEIDs 从若干文本里提取 CVE 编号：大写化、去重、保序、上限 8 个/条
// finding（异常提取不拖垮整份报告）。
func ExtractCVEIDs(texts ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range texts {
		for _, id := range cveRe.FindAllString(strings.ToUpper(t), -1) {
			if seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
			if len(out) >= 8 {
				return out
			}
		}
	}
	return out
}

// cveIntelDesc 截断情报描述（报告里给摘要即可，全文见详情页）。
func cveIntelDesc(d string) string {
	d = strings.TrimSpace(strings.ReplaceAll(d, "\r", ""))
	if len(d) <= 400 {
		return d
	}
	return d[:400] + "…"
}

// cveIntelLineMarkdown 单条情报的 Markdown 富化块。
func cveIntelLineMarkdown(c *CVEIntel) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**%s**", c.CVEID)
	if c.Score != nil {
		fmt.Fprintf(&b, " · CVSS %.1f", *c.Score)
		if c.Vector != "" {
			fmt.Fprintf(&b, "（`%s`）", c.Vector)
		}
	}
	if c.Severity != "" {
		fmt.Fprintf(&b, " · NVD %s", strings.ToUpper(c.Severity))
	}
	if c.KEV {
		b.WriteString(" · **CISA KEV 在野利用**")
	}
	if c.Published != "" {
		fmt.Fprintf(&b, " · 公告 %s", c.Published[:10])
	}
	if t := strings.TrimSpace(c.Title); t != "" {
		fmt.Fprintf(&b, "\n\n> %s", t)
	}
	if d := cveIntelDesc(c.Description); d != "" {
		fmt.Fprintf(&b, "\n\n> %s", d)
	}
	if c.Refs > 0 {
		fmt.Fprintf(&b, "\n\n> 参考：%d 条（详见漏洞情报库 / NVD）", c.Refs)
	}
	b.WriteString("\n\n")
	return b.String()
}

// enrichmentMarkdown 组装一条 finding 的全部情报富化块（无命中返回空串）。
func enrichmentMarkdown(in TaskDocxInput, f *db.DBFinding) string {
	ids := ExtractCVEIDs(f.Name, f.VulnClass, f.Summary)
	if len(ids) == 0 {
		return ""
	}
	var b strings.Builder
	first := true
	for _, id := range ids {
		c, ok := in.CVEInfo[id]
		if !ok || c == nil {
			continue
		}
		if first {
			b.WriteString("**漏洞情报（平台漏洞情报库自动富化）**\n\n")
			first = false
		}
		b.WriteString(cveIntelLineMarkdown(c))
	}
	return b.String()
}

// enrichSeverities 富化块的排序键（报告内无渲染用途，仅为测试与调用方稳定）。
func enrichSeverities(in TaskDocxInput) []string {
	var ids []string
	for id := range in.CVEInfo {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
