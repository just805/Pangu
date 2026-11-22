package report

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/golang/freetype/truetype"
	"github.com/just805/pangu/db"
	"github.com/wcharczuk/go-chart/v2"
)

func sampleFindings() []*db.DBFinding {
	return []*db.DBFinding{
		{ID: 1, Name: "CVE-2023-44487 HTTP/2 Rapid Reset 拒绝服务", VulnClass: "dos", Severity: "high",
			Summary: "目标网关存在 CVE-2023-44487（Rapid Reset）", Status: "confirmed"},
		{ID: 2, Name: "弱口令登录后台", VulnClass: "auth", Severity: "critical",
			Summary: "admin/admin 登录成功", Status: "confirmed"},
		{ID: 3, Name: "目录遍历可读配置", VulnClass: "lfi", Severity: "medium", Summary: "可读 /etc/passwd"},
	}
}

func testInput() TaskDocxInput {
	return TaskDocxInput{
		Title:       "渗透测试报告 — 测试任务",
		Goal:        "验证后台越权",
		GeneratedAt: time.Unix(1763400000, 0).UTC(),
		AssetCounts: map[string]int{"ip": 3},
		Findings:    sampleFindings(),
		CVEInfo: map[string]*CVEIntel{
			"CVE-2023-44487": {CVEID: "CVE-2023-44487", Score: ptr(7.5), Vector: "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:H", KEV: true,
				Severity: "HIGH", Title: "HTTP/2 Rapid Reset", Description: "HTTP/2 Rapid Reset attack.",
				Refs: 5},
		},
		ExecSummary: "本次测试发现 1 个严重、1 个高危漏洞，总体风险偏高；建议立即修复弱口令登录后台。",
	}
}

func ptr(f float64) *float64 { return &f }

// A17：TaskMarkdown 与 docx 同源——执行摘要/富化/复测结构都要出现在 md 里。
func TestTaskMarkdownIncludesExecSummaryAndEnrichment(t *testing.T) {
	md := TaskMarkdown(testInput())
	if !strings.Contains(md, "## 执行摘要") || !strings.Contains(md, "总体风险偏高") {
		t.Fatalf("执行摘要缺失: %s", head(md))
	}
	if !strings.Contains(md, "CVE-2023-44487") || !strings.Contains(md, "CVSS 7.5") ||
		!strings.Contains(md, "CISA KEV 在野利用") {
		t.Fatalf("情报富化缺失: %s", head(md))
	}
	if !strings.Contains(md, "admin/admin 登录成功") || !strings.Contains(md, "弱口令登录后台") {
		t.Fatalf("漏洞明细缺失: %s", head(md))
	}
	if !strings.Contains(md, "五、修复优先级路线图") || !strings.Contains(md, "立即整改") {
		t.Fatalf("路线图缺失: %s", head(md))
	}
}

// 无执行摘要（历史任务）时省略该章，不输出空标题。
func TestTaskMarkdownOmitsEmptyExecSummary(t *testing.T) {
	in := testInput()
	in.ExecSummary = ""
	md := TaskMarkdown(in)
	if strings.Contains(md, "执行摘要") {
		t.Fatalf("空 ExecSummary 不应渲染该章")
	}
}

// A11 提取：大小写归一、去重、跨字段。
func TestExtractCVEIDs(t *testing.T) {
	ids := ExtractCVEIDs("存在 cve-2023-44487 风险", "CVE-2023-44487 / CVE-2021-44228", "无编号")
	if len(ids) != 2 || ids[0] != "CVE-2023-44487" || ids[1] != "CVE-2021-44228" {
		t.Fatalf("ids = %v", ids)
	}
	if got := ExtractCVEIDs("nothing here"); len(got) != 0 {
		t.Fatalf("unexpected ids %v", got)
	}
}

// docXML 解包 docx 的 word/document.xml（zip 压缩后不能直接 strings.Contains）。
func docXML(t *testing.T, d *Docx) string {
	t.Helper()
	var buf bytes.Buffer
	if err := d.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("not a valid zip: %v", err)
	}
	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, _ := f.Open()
		defer rc.Close()
		var b bytes.Buffer
		_, _ = b.ReadFrom(rc)
		return b.String()
	}
	t.Fatalf("word/document.xml missing")
	return ""
}

// A13：docx 也渲染执行摘要章与富化块（md 与 docx 同源验证）。
func TestBuildTaskDocxExecSummaryAndEnrichment(t *testing.T) {
	doc := docXML(t, BuildTaskDocx(testInput()))
	if !strings.Contains(doc, "总体风险偏高") {
		t.Fatalf("docx 缺执行摘要内容")
	}
	if !strings.Contains(doc, "平台情报库自动富化") || !strings.Contains(doc, "CVSS 7.5") {
		t.Fatalf("docx 缺情报富化块")
	}
}

// A6：统计图 PNG 生成非空且可解码；缺字体环境返回 nil（降级不出图）。
func TestChartsRender(t *testing.T) {
	if f := chartCJKFont(); f == nil {
		b, err := gongwenFontFS.ReadFile("fonts/fangsong_gb2312.ttf")
		if err != nil {
			t.Skipf("font embed missing: %v", err)
		}
		_, perr := truetype.Parse(b)
		t.Skipf("truetype parse failed: %v", perr)
	}
	png, err := renderBarChart("漏洞严重度分布", []chart.Value{{Value: 2, Label: "高危"}})
	if err != nil {
		t.Fatalf("renderBarChart: %v", err)
	}
	_ = png
	if cp := severityDistChart(sampleFindings()); cp == nil || len(cp) == 0 || !strings.HasPrefix(string(cp[:4]), "\x89PNG") {
		t.Fatalf("severity chart not a valid PNG (len=%d)", len(cp))
	}
	if cp := classDistChart(sampleFindings()); cp == nil || len(cp) == 0 {
		t.Fatalf("class chart empty")
	}
}

func head(s string) string {
	if len(s) > 400 {
		return s[:400]
	}
	return s
}
