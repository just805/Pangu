package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/just805/pangu/db"
	"github.com/just805/pangu/vulnintel"
)

// 情报桥的两个短路分支（无 store / 无 trivy 行）不需要数据库。
func TestMatchPackageVulnsNoStore(t *testing.T) {
	SetVulnIntelStore(nil)
	rows := []db.CodeScanResult{
		{Scanner: "trivy", PackageName: "lodash", PackageVersion: "4.0.0"},
	}
	if got := matchPackageVulns(context.Background(), rows); got != nil {
		t.Errorf("情报库未接入时应返回 nil，实际 %+v", got)
	}
	// 只有非 trivy 的行同样返回 nil。
	SetVulnIntelStore(&vulnintel.Store{})
	if got := matchPackageVulns(context.Background(), []db.CodeScanResult{
		{Scanner: "semgrep", FilePath: "a.go"},
	}); got != nil {
		t.Errorf("没有成分分析行时应返回 nil，实际 %+v", got)
	}
	SetVulnIntelStore(nil)
}

// 真库验证：组件 + 版本 → 公开 CVE，并确认版本区间判定被带出来。
//
// 这条测的是「桥通不通」，不是 vulnintel 的检索质量（那边已有测试）。
// 关键点：包名当 product 传、**不**把 ecosystem 当 vendor 传——后者是包含匹配
// 过滤器，传 "npm" 会把 vendor 为空的 npm 包命中全部过滤掉。
func TestMatchPackageVulnsAgainstIntel(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("PANGU_PG_DSN"))
	if dsn == "" {
		t.Skip("requires PANGU_PG_DSN")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer d.Close()
	ctx := context.Background()

	SetVulnIntelStore(vulnintel.New(d))
	t.Cleanup(func() { SetVulnIntelStore(nil) })

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	pkg := "testpkg" + suffix
	cve := "CVE-2099-" + suffix[len(suffix)-6:]

	if _, err := d.ExecContext(ctx, `INSERT INTO vuln_cves(cve_id, source, title, severity, cvss_score)
		VALUES ($1,'nvd','bridge test','HIGH',8.1)`, cve); err != nil {
		t.Fatalf("造 CVE: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM vuln_cves WHERE cve_id=$1`, cve) })
	if _, err := d.ExecContext(ctx, `INSERT INTO vuln_cpe_products(cve_id, vendor, product, version_end_excl)
		VALUES ($1,'',$2,'2.0.0')`, cve, pkg); err != nil {
		t.Fatalf("造 CPE 产品行: %v", err)
	}

	rows := []db.CodeScanResult{
		{Scanner: "trivy", PackageName: pkg, PackageVersion: "1.5.0", PackageEcosystem: "npm"},
	}
	got := matchPackageVulns(ctx, rows)
	if len(got) != 1 {
		t.Fatalf("应命中 1 个包，实际 %d：%+v", len(got), got)
	}
	if got[0]["package"] != pkg || got[0]["ecosystem"] != "npm" {
		t.Errorf("包信息不符: %+v", got[0])
	}
	cves, _ := got[0]["cves"].([]map[string]any)
	if len(cves) == 0 {
		t.Fatalf("应命中 CVE: %+v", got[0])
	}
	inRange, ok := cves[0]["version_in_range"].(bool)
	if !ok {
		t.Fatalf("应带出版本区间判定（version_end_excl=2.0.0 且版本 1.5.0）: %+v", cves[0])
	}
	if !inRange {
		t.Errorf("1.5.0 应落在 <2.0.0 区间内，实际 version_in_range=false")
	}

	// 区间外版本应判 false —— 这是「版本匹配 ≠ 存在漏洞」的机器化表达。
	got2 := matchPackageVulns(ctx, []db.CodeScanResult{
		{Scanner: "trivy", PackageName: pkg, PackageVersion: "3.0.0", PackageEcosystem: "npm"},
	})
	if len(got2) == 0 {
		t.Fatal("区间外版本仍应返回命中（供模型判断），不能静默吞掉")
	}
	cves2, _ := got2[0]["cves"].([]map[string]any)
	if len(cves2) == 0 {
		t.Fatal("区间外版本应仍列出 CVE")
	}
	if v, ok := cves2[0]["version_in_range"].(bool); ok && v {
		t.Errorf("3.0.0 不应落在 <2.0.0 区间内")
	}
}
