package report

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"

	"github.com/just805/pangu/db"
)

// ═══ M-10：漏洞清单 Excel 导出（手写最小 xlsx，零第三方库）═══
//
// 甲方资产台账最常用的导入格式就是 Excel：一漏洞一行、字段固定。
// 与 Word 报告同一数据源（ListFindingsForExport），只是形态不同。
// 用 inlineStr 写字符串，避免 sharedStrings 表——最小实现、无依赖。

// FindingsXLSX 把漏洞清单渲染成 xlsx 字节流。
func FindingsXLSX(findings []*db.DBFinding, assetNames map[int64]string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// [Content_Types].xml
	if err := addZipFile(zw, "[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>
<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>
</Types>`); err != nil {
		return nil, err
	}
	// _rels/.rels
	if err := addZipFile(zw, "_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>
</Relationships>`); err != nil {
		return nil, err
	}
	// xl/workbook.xml
	if err := addZipFile(zw, "xl/workbook.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"
 xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
<sheets><sheet name="漏洞清单" sheetId="1" r:id="rId1"/></sheets>
</workbook>`); err != nil {
		return nil, err
	}
	// xl/_rels/workbook.xml.rels
	if err := addZipFile(zw, "xl/_rels/workbook.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>
</Relationships>`); err != nil {
		return nil, err
	}
	// xl/worksheets/sheet1.xml
	if err := addZipFile(zw, "xl/worksheets/sheet1.xml", buildFindingsSheet(findings, assetNames)); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func addZipFile(zw *zip.Writer, name, body string) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write([]byte(body))
	return err
}

// buildFindingsSheet 生成 sheet1.xml。列顺序与甲方台账习惯一致：
// 严重度 → 类别 → 名称 → CVSS → 影响资产 → 状态 → 发现时间 → 摘要。
func buildFindingsSheet(findings []*db.DBFinding, assetNames map[int64]string) string {
	headers := []string{"严重度", "漏洞类别", "漏洞名称", "CVSS", "影响资产", "状态", "发现时间", "摘要"}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	// 列宽：名称/摘要宽一些，其余适中。
	b.WriteString(`<cols><col min="1" max="1" width="10"/><col min="2" max="2" width="16"/>` +
		`<col min="3" max="3" width="40"/><col min="4" max="4" width="8"/>` +
		`<col min="5" max="5" width="32"/><col min="6" max="6" width="10"/>` +
		`<col min="7" max="7" width="20"/><col min="8" max="8" width="50"/></cols>`)
	b.WriteString(`<sheetData>`)
	b.WriteString(sheetRow(1, headers))
	for i, f := range findings {
		cvss := ""
		if f.CVSSScore != nil {
			cvss = fmt.Sprintf("%.1f", *f.CVSSScore)
		}
		cells := []string{
			f.Severity,
			f.VulnClass,
			f.Name,
			cvss,
			assetNamesOf(f.AssetIDs, assetNames),
			f.Status,
			FormatTimeUTC(f.CreatedAt),
			oneLine(f.Summary),
		}
		b.WriteString(sheetRow(i+2, cells))
	}
	b.WriteString(`</sheetData></worksheet>`)
	return b.String()
}

// sheetRow 渲染一行（1 基行号），全部用 inlineStr，避免共享字符串表。
func sheetRow(rowNo int, cells []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<row r="%d">`, rowNo)
	for i, v := range cells {
		ref := fmt.Sprintf("%s%d", colName(i), rowNo)
		fmt.Fprintf(&b, `<c r="%s" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`,
			ref, xmlEscape(v))
	}
	b.WriteString(`</row>`)
	return b.String()
}

// colName 把 0 基列号转成 A/B/…/Z/AA 形式。
func colName(i int) string {
	name := ""
	for i >= 0 {
		name = string(rune('A'+i%26)) + name
		i = i/26 - 1
	}
	return name
}

func xmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;", "<", "&lt;", ">", "&gt;",
		`"`, "&quot;", "'", "&apos;",
	)
	return r.Replace(s)
}

// oneLine 把多行摘要压成一行，避免单元格里出现换行（Excel 里会撑高行）。
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.TrimSpace(s)
}

// assetNamesOf 渲染资产名（最多 5 个，其余计数）。
func assetNamesOf(ids []int64, names map[int64]string) string {
	parts := make([]string, 0, len(ids))
	for i, id := range ids {
		if i >= 5 {
			parts = append(parts, fmt.Sprintf("…等 %d 个", len(ids)))
			break
		}
		if n := strings.TrimSpace(names[id]); n != "" {
			parts = append(parts, n)
		} else {
			parts = append(parts, fmt.Sprintf("#%d", id))
		}
	}
	return strings.Join(parts, "、")
}
