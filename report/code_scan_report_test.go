package report

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/just805/pangu/db"
)

// docText 取 docx 里 document.xml 的纯文本（与 compliance 测试同一手法）。
func docText(t *testing.T, in TaskDocxInput) string {
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

func sampleCodeScans() []db.CodeScanBrief {
	line := 42
	return []db.CodeScanBrief{{
		AssetID: 7, AssetName: "order-service", Location: "/src/order",
		Total: 5,
		ByScanner:  map[string]int{"semgrep": 4, "gitleaks": 1},
		BySeverity: map[string]int{"high": 2, "medium": 3},
		TopSinks: []db.CodeScanResult{
			{FilePath: "internal/dao/order.go", LineStart: &line, RuleID: "go.lang.security.sqli", Severity: "high"},
		},
	}}
}

// 静态扫描结果必须出现在交付件里，且措辞强调「线索不是结论」。
func TestCodeScanChapterRender(t *testing.T) {
	doc := docText(t, TaskDocxInput{
		Title: "T", Goal: "G", GeneratedAt: time.Now(),
		CodeScans: sampleCodeScans(),
	})
	for _, frag := range []string{
		"八、静态扫描线索", "order-service", "共 5 条线索",
		"semgrep 4", "high 2", "internal/dao/order.go:42", "线索不是结论",
	} {
		if !strings.Contains(doc, frag) {
			t.Errorf("document.xml 缺少 %q", frag)
		}
	}
}

// 没登记/没扫描源码的任务不该凭空多出一章。
func TestCodeScanChapterOmittedWithoutData(t *testing.T) {
	doc := docText(t, TaskDocxInput{Title: "T", Goal: "G", GeneratedAt: time.Now()})
	if strings.Contains(doc, "静态扫描线索") {
		t.Error("没有扫描数据时不应渲染该章")
	}
}

// A17 同源：Markdown 预览与 Word 是同一章同一口径。
func TestTaskMarkdownCodeScanParity(t *testing.T) {
	in := TaskDocxInput{Title: "T", GeneratedAt: time.Now(), CodeScans: sampleCodeScans()}
	md := TaskMarkdown(in)
	for _, frag := range []string{"八、静态扫描线索", "order-service", "共 5 条线索", "go.lang.security.sqli"} {
		if !strings.Contains(md, frag) {
			t.Errorf("Markdown 缺少 %q", frag)
		}
	}
	if strings.Contains(TaskMarkdown(TaskDocxInput{Title: "T", GeneratedAt: time.Now()}), "八、静态扫描线索") {
		t.Error("无数据时 Markdown 不应渲染该章")
	}
}

// distText 必须排序输出——否则同一份数据每次排版顺序不同，交付件不可复现。
func TestDistTextSorted(t *testing.T) {
	got := distText(map[string]int{"semgrep": 4, "gitleaks": 1, "trivy": 2})
	if got != "gitleaks 1 · semgrep 4 · trivy 2" {
		t.Errorf("distText 未按字典序输出：%q", got)
	}
	if distText(nil) != "无" {
		t.Errorf("空分布应显示「无」，实际 %q", distText(nil))
	}
	// 空键（严重度缺失）要有可读标签，不能打印空白。
	if d := distText(map[string]int{"": 2}); d != "未标注 2" {
		t.Errorf("空键应显示「未标注」，实际 %q", d)
	}
}
