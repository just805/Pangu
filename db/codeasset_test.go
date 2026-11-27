package db

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestSplitHostAndKeyword(t *testing.T) {
	cases := []struct{ in, host, kw string }{
		{"https://api.example.com/api/order/create?id=1", "api.example.com", "create"},
		{"https://api.example.com/users/12345", "api.example.com", "users"},
		{"api.example.com:8443", "api.example.com", ""},
		{"https://x.com/", "x.com", ""},
		{"https://x.com/a/7f3c9e2a-1111-2222-3333-444455556666", "x.com", "a"},
	}
	for _, c := range cases {
		host, kw := splitHostAndKeyword(c.in)
		if host != c.host || kw != c.kw {
			t.Errorf("splitHostAndKeyword(%q) = (%q,%q)，期望 (%q,%q)", c.in, host, kw, c.host, c.kw)
		}
	}
}

// newCodeAssetTestDB 连接测试库（与 db 包其余测试同一套约定：无 DSN 就 skip）。
func newCodeAssetTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// newCodeAssetFixture 建一个独立的代码资产，测试结束清掉（含级联的扫描结果）。
func newCodeAssetFixture(t *testing.T, d *DB, name string) *CodeAssetStore {
	t.Helper()
	s := d.CodeAssets()
	id, err := s.UpsertCodeAsset(context.Background(), CodeAsset{
		Name: name, Kind: "repo", Location: "/tmp/" + name, Language: "go",
	})
	if err != nil {
		t.Fatalf("登记代码资产: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM code_assets WHERE id = $1`, id)
	})
	return s
}

func TestCodeAssetUpsertIsIdempotent(t *testing.T) {
	d := newCodeAssetTestDB(t)
	ctx := context.Background()
	name := fmt.Sprintf("idem-%d", time.Now().UnixNano())
	s := newCodeAssetFixture(t, d, name)

	// 同名同类型再登记一次：应更新而不是新建。
	id2, err := s.UpsertCodeAsset(ctx, CodeAsset{Name: name, Kind: "repo", Language: "java"})
	if err != nil {
		t.Fatalf("重复登记: %v", err)
	}
	got, err := s.GetCodeAsset(ctx, id2)
	if err != nil || got == nil {
		t.Fatalf("读取: %v", err)
	}
	if got.Language != "java" {
		t.Errorf("冲突时应补更新非空字段，language=%q", got.Language)
	}
	// 空字段不该覆盖已有值。
	if got.Location != "/tmp/"+name {
		t.Errorf("空 location 覆盖了已有值：%q", got.Location)
	}
}

func TestCodeScanIngestDedupAndSummary(t *testing.T) {
	d := newCodeAssetTestDB(t)
	ctx := context.Background()
	name := fmt.Sprintf("scan-%d", time.Now().UnixNano())
	s := newCodeAssetFixture(t, d, name)
	asset, err := s.ListCodeAssets(ctx, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	var assetID int64
	for _, a := range asset {
		if a.Name == name {
			assetID = a.ID
		}
	}
	if assetID == 0 {
		t.Fatal("没找到刚登记的资产")
	}

	rows := []CodeScanResult{
		{Scanner: "semgrep", RuleID: "r1", Severity: "high", FilePath: "a.go", Fingerprint: "fp1"},
		{Scanner: "semgrep", RuleID: "r1", Severity: "high", FilePath: "b.go", Fingerprint: "fp2"},
		{Scanner: "trivy", RuleID: "CVE-2024-1", Severity: "critical", PackageName: "x", PackageVersion: "1.0", Fingerprint: "fp3"},
	}
	ins, upd, err := s.InsertCodeScanResults(ctx, assetID, rows)
	if err != nil {
		t.Fatalf("入库: %v", err)
	}
	if ins != 3 || upd != 0 {
		t.Errorf("首次应新增 3 条，实际 inserted=%d updated=%d", ins, upd)
	}

	// 同一批再来一次：指纹相同 → 全部走更新，不堆行。
	ins2, upd2, err := s.InsertCodeScanResults(ctx, assetID, rows)
	if err != nil {
		t.Fatalf("重复入库: %v", err)
	}
	if ins2 != 0 || upd2 != 3 {
		t.Errorf("重复入库应全部更新，实际 inserted=%d updated=%d", ins2, upd2)
	}

	sum, err := s.CodeScanSummary(ctx, assetID)
	if err != nil {
		t.Fatal(err)
	}
	if sum["total"] != 3 {
		t.Errorf("去重后总数应为 3，实际 %v", sum["total"])
	}

	// 关键词搜索：命中文件路径。
	sinks, err := s.ListCodeSinks(ctx, assetID, "b.go", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sinks) != 1 || sinks[0].FilePath != "b.go" {
		t.Errorf("关键词应命中 b.go，实际 %+v", sinks)
	}
}

// 反查链路：无归属的 URL 只应看到「全局登记」的仓库，不能把别的企业的源码带出来。
func TestCodeContextForUnattributed(t *testing.T) {
	d := newCodeAssetTestDB(t)
	ctx := context.Background()
	name := fmt.Sprintf("ctx-%d", time.Now().UnixNano())
	s := newCodeAssetFixture(t, d, name)

	list, _ := s.ListCodeAssets(ctx, 0, 200)
	var assetID int64
	for _, a := range list {
		if a.Name == name {
			assetID = a.ID
		}
	}
	if assetID == 0 {
		t.Fatal("没找到刚登记的资产")
	}
	if _, _, err := s.InsertCodeScanResults(ctx, assetID, []CodeScanResult{
		{Scanner: "semgrep", RuleID: "r", Severity: "high", FilePath: "handlers/order_create.go",
			Snippet: "func CreateOrder(c *gin.Context)", Fingerprint: "ctx-fp-1"},
	}); err != nil {
		t.Fatal(err)
	}

	// 一个没登记的域名：应退化为「全局登记」仓库，并按路径关键词 create 搜到 sink。
	cc, err := s.CodeContextFor(ctx, "https://not-registered.example/api/order/create", 10)
	if err != nil {
		t.Fatalf("反查失败: %v", err)
	}
	if cc.Keyword != "create" {
		t.Errorf("关键词应取路径末段 create，实际 %q", cc.Keyword)
	}
	found := false
	for _, a := range cc.Assets {
		if a.Name == name {
			found = true
		}
	}
	if !found {
		t.Errorf("应命中全局登记的仓库，实际 %d 个", len(cc.Assets))
	}
	if len(cc.Sinks) == 0 {
		t.Error("应命中 file_path 含 create 的 sink")
	}
	if cc.Note == "" {
		t.Error("应给出提示语（静态线索不是漏洞）")
	}

	// 无归属时不能返回有企业归属的仓库。
	company := fmt.Sprintf("cmp-%d", time.Now().UnixNano())
	var companyID int64
	if err := d.QueryRowContext(ctx, `INSERT INTO companies(name, nkey) VALUES ($1,$2) RETURNING id`,
		company, company).Scan(&companyID); err == nil {
		t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM companies WHERE id=$1`, companyID) })
		owned := fmt.Sprintf("owned-%d", time.Now().UnixNano())
		if id, err := s.UpsertCodeAsset(ctx, CodeAsset{CompanyID: &companyID, Name: owned, Kind: "repo"}); err == nil {
			t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM code_assets WHERE id=$1`, id) })
			if cc2, err := s.CodeContextFor(ctx, "https://another.example/x", 10); err == nil {
				for _, a := range cc2.Assets {
					if a.Name == owned {
						t.Error("无归属反查不应返回其他企业的仓库")
					}
				}
			}
		}
	}
}

// 没有指纹的行不能入库——否则去重键失效，重复扫描会堆行。
func TestCodeScanIngestSkipsEmptyFingerprint(t *testing.T) {
	d := newCodeAssetTestDB(t)
	ctx := context.Background()
	name := fmt.Sprintf("nofp-%d", time.Now().UnixNano())
	s := newCodeAssetFixture(t, d, name)
	list, _ := s.ListCodeAssets(ctx, 0, 200)
	var assetID int64
	for _, a := range list {
		if a.Name == name {
			assetID = a.ID
		}
	}
	ins, _, err := s.InsertCodeScanResults(ctx, assetID, []CodeScanResult{
		{Scanner: "semgrep", RuleID: "r", Severity: "high"}, // 无指纹
	})
	if err != nil {
		t.Fatal(err)
	}
	if ins != 0 {
		t.Errorf("无指纹的行不应入库，实际 inserted=%d", ins)
	}
}
