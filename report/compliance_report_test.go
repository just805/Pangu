package report

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
	"time"
)

// complianceDocText 取出 docx 里 document.xml 的纯文本，供章节断言。
func complianceDocText(t *testing.T, in TaskDocxInput) string {
	t.Helper()
	var buf bytes.Buffer
	if err := BuildTaskDocx(in).Encode(&buf); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, zf := range zr.File {
		if zf.Name != "word/document.xml" {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		_, _ = b.ReadFrom(rc)
		rc.Close()
		return b.String()
	}
	return ""
}

// TestComplianceChapterRender 锁住 M-09 的最后一公里：合规结果必须出现在交付件里，
// 且「未核查」不能被写成「符合」。
func TestComplianceChapterRender(t *testing.T) {
	in := TaskDocxInput{
		Title: "T", Goal: "G", GeneratedAt: time.Now(),
		Compliance: []ComplianceBaseline{{
			Name: "cis-web", Label: "CIS Web 服务器基线（裁剪）", Total: 3,
			Lines: []ComplianceLine{
				{ItemKey: "hdr-1", Title: "安全响应头", Expect: "有 HSTS", Status: "pass", Evidence: "HSTS: max-age=31536000"},
				{ItemKey: "dir-1", Title: "目录列表", Expect: "不返回索引", Status: "fail", Evidence: "GET /uploads/ → 200 目录索引"},
				{ItemKey: "cookie-1", Title: "Cookie 安全属性", Expect: "HttpOnly", Status: ""},
			},
		}},
	}
	doc := complianceDocText(t, in)
	for _, frag := range []string{"七、合规基线核查", "CIS Web 服务器基线", "安全响应头", "目录列表", "Cookie 安全属性", "不符合", "未核查"} {
		if !strings.Contains(doc, frag) {
			t.Errorf("document.xml 缺少 %q", frag)
		}
	}
	// 空状态的那一项必须落「未核查」——不能被当成通过。
	if strings.Count(doc, "未核查") < 2 {
		t.Errorf("未核查 计数异常：统计行 + 表格单元格至少应出现 2 次")
	}
}

// TestComplianceChapterOmittedWithoutData：历史任务没有核查记录，报告不该多出一章。
func TestComplianceChapterOmittedWithoutData(t *testing.T) {
	doc := complianceDocText(t, TaskDocxInput{Title: "T", Goal: "G", GeneratedAt: time.Now()})
	if strings.Contains(doc, "合规基线核查") {
		t.Error("没有核查数据时不应渲染合规章")
	}
}

// TestTaskMarkdownComplianceParity：A17 同源原则——Markdown 预览与 Word 同一章同一口径。
func TestTaskMarkdownComplianceParity(t *testing.T) {
	in := TaskDocxInput{
		Title: "T", GeneratedAt: time.Now(),
		Compliance: []ComplianceBaseline{{
			Name: "djbh2", Label: "等保 2.0 二级（裁剪）", Total: 2,
			Lines: []ComplianceLine{
				{ItemKey: "ident-1", Title: "登录失败处理", Status: "fail", Evidence: "无限重试"},
				{ItemKey: "audit-1", Title: "日志留存", Status: "na"},
			},
		}},
	}
	md := TaskMarkdown(in)
	for _, frag := range []string{"七、合规基线核查", "等保 2.0 二级", "登录失败处理", "| 不符合 |", "不适用"} {
		if !strings.Contains(md, frag) {
			t.Errorf("Markdown 缺少 %q", frag)
		}
	}
	// 没有数据时不出现该章。
	if strings.Contains(TaskMarkdown(TaskDocxInput{Title: "T", GeneratedAt: time.Now()}), "合规基线核查") {
		t.Error("无数据时 Markdown 不应渲染合规章")
	}
}

// TestComplianceCounts 覆盖四态统计，避免两个视图各算一遍后数字漂移。
func TestComplianceCounts(t *testing.T) {
	pass, fail, na, unknown := complianceCounts([]ComplianceLine{
		{Status: "pass"}, {Status: "PASS"}, {Status: "fail"}, {Status: "na"}, {Status: "unknown"}, {Status: ""},
	})
	if pass != 2 || fail != 1 || na != 1 || unknown != 2 {
		t.Errorf("统计不符: pass=%d fail=%d na=%d unknown=%d", pass, fail, na, unknown)
	}
}
