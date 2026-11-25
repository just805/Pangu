package db

import (
	"context"
	"testing"
)

func TestAssetSnapshotDiff(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	defer d.Close()
	ctx := context.Background()
	store := d.AssetSnapshots()
	const scope = "company:999901"
	// 清理：快照按 scope_key，资产按固定域名。
	_, _ = d.Exec(`DELETE FROM asset_snapshots WHERE scope_key=$1`, scope)
	_, _ = d.Exec(`DELETE FROM assets WHERE domain LIKE 'snap-test-%'`)
	defer d.Exec(`DELETE FROM asset_snapshots WHERE scope_key=$1`, scope)
	defer d.Exec(`DELETE FROM assets WHERE domain LIKE 'snap-test-%'`)

	// 企业必须先存在（assets.company_id 有外键）。
	var companyID int64
	if err := d.QueryRow(`INSERT INTO companies(name, nkey) VALUES('snap-co','snap-co-test') RETURNING id`).Scan(&companyID); err != nil {
		t.Fatalf("create company: %v", err)
	}
	defer d.Exec(`DELETE FROM companies WHERE id=$1`, companyID)
	// 用真实 company id 重定 scope。
	scopeReal := "company:" + itoa64(companyID)
	_, _ = d.Exec(`DELETE FROM asset_snapshots WHERE scope_key=$1`, scopeReal)
	defer d.Exec(`DELETE FROM asset_snapshots WHERE scope_key=$1`, scopeReal)

	ins := func(domain, title string) int64 {
		var id int64
		if err := d.QueryRow(
			`INSERT INTO assets(type, domain, page_title, company_id) VALUES('subdomain',$1,$2,$3) RETURNING id`,
			domain, title, companyID).Scan(&id); err != nil {
			t.Fatalf("insert asset %s: %v", domain, err)
		}
		return id
	}
	a := ins("snap-test-a.example", "首页")
	ins("snap-test-b.example", "登录")

	snap1, err := store.TakeSnapshot(ctx, scopeReal)
	if err != nil {
		t.Fatalf("take snapshot 1: %v", err)
	}
	if snap1.AssetCount != 2 {
		t.Fatalf("快照1 应有 2 条资产，实际 %d", snap1.AssetCount)
	}
	// 同内容再采一次：digest 应一致。
	snap1b, err := store.TakeSnapshot(ctx, scopeReal)
	if err != nil {
		t.Fatalf("take snapshot 1b: %v", err)
	}
	if snap1b.Digest != snap1.Digest {
		t.Errorf("同内容快照 digest 应一致: %s vs %s", snap1.Digest, snap1b.Digest)
	}

	// 变化：a 的标题变了；新增 c。
	if _, err := d.Exec(`UPDATE assets SET page_title='首页 v2' WHERE id=$1`, a); err != nil {
		t.Fatalf("update asset: %v", err)
	}
	ins("snap-test-c.example", "后台")

	snap2, err := store.TakeSnapshot(ctx, scopeReal)
	if err != nil {
		t.Fatalf("take snapshot 2: %v", err)
	}
	diff, err := store.Diff(ctx, 0, snap2.ID) // base 自动取上一个同范围快照
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if diff.BaseID != snap1b.ID {
		t.Errorf("base 应自动取上一个快照 %d，实际 %d", snap1b.ID, diff.BaseID)
	}
	if len(diff.Added) != 1 {
		t.Errorf("应有 1 条新增，实际 %d: %+v", len(diff.Added), diff.Added)
	}
	if len(diff.Changed) != 1 {
		t.Errorf("应有 1 条变化，实际 %d: %+v", len(diff.Changed), diff.Changed)
	}
	if len(diff.Removed) != 0 {
		t.Errorf("不应有消失项，实际 %d", len(diff.Removed))
	}

	// 删除 b 后再采：应出现消失项。
	if _, err := d.Exec(`DELETE FROM assets WHERE domain='snap-test-b.example'`); err != nil {
		t.Fatalf("delete asset: %v", err)
	}
	snap3, err := store.TakeSnapshot(ctx, scopeReal)
	if err != nil {
		t.Fatalf("take snapshot 3: %v", err)
	}
	diff2, err := store.Diff(ctx, 0, snap3.ID)
	if err != nil {
		t.Fatalf("diff 2: %v", err)
	}
	if len(diff2.Removed) != 1 {
		t.Errorf("应有 1 条消失，实际 %d: %+v", len(diff2.Removed), diff2.Removed)
	}
}

func TestAssetSnapshotNoBase(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	defer d.Close()
	ctx := context.Background()
	store := d.AssetSnapshots()
	const scope = "company:999902"
	_, _ = d.Exec(`DELETE FROM asset_snapshots WHERE scope_key=$1`, scope)
	defer d.Exec(`DELETE FROM asset_snapshots WHERE scope_key=$1`, scope)
	snap, err := store.TakeSnapshot(ctx, scope)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if _, err := store.Diff(ctx, 0, snap.ID); err == nil {
		t.Fatal("只有一次快照时 diff 应报错（没有可比对的基线）")
	}
}

func itoa64(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
