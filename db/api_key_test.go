package db

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAPIKeyLifecycle(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	defer d.Close()
	ctx := context.Background()
	name := "api-key-test"
	_, _ = d.Exec(`DELETE FROM api_keys WHERE name=$1`, name)
	defer d.Exec(`DELETE FROM api_keys WHERE name=$1`, name)

	key, raw, err := d.CreateAPIKey(ctx, name, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasPrefix(raw, "pk_") {
		t.Fatalf("明文应以 pk_ 开头: %q", raw)
	}
	if key.KeyHint == "" || !strings.HasSuffix(key.KeyHint, raw[len(raw)-4:]) {
		t.Errorf("hint 应保留末 4 位: %q vs %q", key.KeyHint, raw)
	}
	// 正确 Key 通过。
	if !d.VerifyAPIKey(ctx, raw) {
		t.Fatal("正确 Key 应通过")
	}
	// 错误 Key 不通过。
	if d.VerifyAPIKey(ctx, "pk_deadbeef") {
		t.Fatal("错误 Key 不应通过")
	}
	// 非前缀不通过（例如误把 JWT 传进来）。
	if d.VerifyAPIKey(ctx, "eyJhbGciOi") {
		t.Fatal("非 pk_ 前缀不应通过")
	}
	// last_used_at 已更新。
	list, err := d.ListAPIKeys(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, k := range list {
		if k.ID == key.ID {
			found = true
			if k.LastUsedAt == nil {
				t.Error("验证后 last_used_at 应被更新")
			}
		}
	}
	if !found {
		t.Fatal("list 未返回刚创建的 Key")
	}
	// 删除后立即失效。
	if err := d.DeleteAPIKey(ctx, key.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if d.VerifyAPIKey(ctx, raw) {
		t.Fatal("删除后 Key 不应再通过")
	}
}

func TestAPIKeyExpiry(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	defer d.Close()
	ctx := context.Background()
	name := "api-key-expired"
	_, _ = d.Exec(`DELETE FROM api_keys WHERE name=$1`, name)
	defer d.Exec(`DELETE FROM api_keys WHERE name=$1`, name)

	past := time.Now().Add(-time.Hour)
	_, raw, err := d.CreateAPIKey(ctx, name, &past)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if d.VerifyAPIKey(ctx, raw) {
		t.Fatal("已过期的 Key 不应通过")
	}

	future := time.Now().Add(24 * time.Hour)
	_, raw2, err := d.CreateAPIKey(ctx, name+"-ok", &future)
	if err != nil {
		t.Fatalf("create ok: %v", err)
	}
	defer d.Exec(`DELETE FROM api_keys WHERE name=$1`, name+"-ok")
	if !d.VerifyAPIKey(ctx, raw2) {
		t.Fatal("未过期的 Key 应通过")
	}
}
