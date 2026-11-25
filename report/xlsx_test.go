package report

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/just805/pangu/db"
)

func score(v float64) *float64 { return &v }

func TestFindingsXLSXStructure(t *testing.T) {
	findings := []*db.DBFinding{
		{ID: 1, Severity: "high", VulnClass: "sql_injection", Name: "登录接口 SQL 注入",
			Summary: "可注入\n能读全库", Status: "confirmed", CreatedAt: time.Unix(1700000000, 0),
			AssetIDs: []int64{7}, CVSSScore: score(8.8)},
		{ID: 2, Severity: "low", VulnClass: "info_leak", Name: "版本泄露",
			Summary: "仅版本号", Status: "pending", CreatedAt: time.Unix(1700000000, 0)},
	}
	data, err := FindingsXLSX(findings, map[int64]string{7: "api.example.com"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// 必须是合法 zip，且包含最小 xlsx 的五个部件。
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("不是合法 zip: %v", err)
	}
	want := map[string]bool{
		"[Content_Types].xml": false, "_rels/.rels": false,
		"xl/workbook.xml": false, "xl/_rels/workbook.xml.rels": false,
		"xl/worksheets/sheet1.xml": false,
	}
	for _, f := range zr.File {
		if _, ok := want[f.Name]; ok {
			want[f.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("缺少部件 %s", name)
		}
	}
	// 表头与两行数据：行号 r="1".."3"。
	sheet := zipEntry(t, zr, "xl/worksheets/sheet1.xml")
	for _, h := range []string{"严重度", "漏洞类别", "漏洞名称", "CVSS", "影响资产"} {
		if !strings.Contains(sheet, h) {
			t.Errorf("表头缺少 %s", h)
		}
	}
	if !strings.Contains(sheet, `r="3"`) {
		t.Error("应有第 3 行（表头 + 2 条漏洞）")
	}
	// CVSS 分值渲染成一位小数。
	if !strings.Contains(sheet, "8.8") {
		t.Error("CVSS 分值应写入单元格")
	}
	// 摘要里的换行应被压成空格（Excel 单元格不撑高）。
	if strings.Contains(sheet, "可注入\n能读全库") {
		t.Error("摘要换行应被压平")
	}
	// XML 特殊字符要转义。
	evil, err := FindingsXLSX([]*db.DBFinding{{ID: 3, Name: `a<b&c"d`, Severity: "low"}}, nil)
	if err != nil {
		t.Fatalf("escape case: %v", err)
	}
	zr2, _ := zip.NewReader(bytes.NewReader(evil), int64(len(evil)))
	s2 := zipEntry(t, zr2, "xl/worksheets/sheet1.xml")
	if strings.Contains(s2, "<b&") {
		t.Error("XML 特殊字符未转义")
	}
	if !strings.Contains(s2, "&lt;") || !strings.Contains(s2, "&amp;") {
		t.Error("应包含转义后的实体")
	}
}

func TestColName(t *testing.T) {
	cases := map[int]string{0: "A", 1: "B", 25: "Z", 26: "AA", 27: "AB", 51: "AZ", 52: "BA"}
	for i, want := range cases {
		if got := colName(i); got != want {
			t.Errorf("colName(%d) = %s, want %s", i, got, want)
		}
	}
}

// zipEntry 读取 zip 内的文本部件。
func zipEntry(t *testing.T, zr *zip.Reader, name string) string {
	t.Helper()
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		defer rc.Close()
		b, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(b)
	}
	t.Fatalf("zip 内没有 %s", name)
	return ""
}
