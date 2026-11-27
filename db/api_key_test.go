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

	key, raw, err := d.CreateAPIKey(ctx, name, APIKeyScopeWrite, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasPrefix(raw, "pk_") {
		t.Fatalf("明文应以 pk_ 开头: %q", raw)
	}
	if key.KeyHint == "" || !strings.HasSuffix(key.KeyHint, raw[len(raw)-4:]) {
		t.Errorf("hint 应保留末 4 位: %q vs %q", key.KeyHint, raw)
	}
	if key.Scope != APIKeyScopeWrite {
		t.Errorf("scope 应为 %q，实际 %q", APIKeyScopeWrite, key.Scope)
	}
	// 正确 Key 通过，并带回 scope 与来源地址。
	auth, ok := d.VerifyAPIKey(ctx, raw, "10.0.0.9")
	if !ok {
		t.Fatal("正确 Key 应通过")
	}
	if auth.ID != key.ID || auth.Scope != APIKeyScopeWrite {
		t.Errorf("授权信息不符: %+v", auth)
	}
	// 错误 Key 不通过。
	if _, ok := d.VerifyAPIKey(ctx, "pk_deadbeef", ""); ok {
		t.Fatal("错误 Key 不应通过")
	}
	// 非前缀不通过（例如误把 JWT 传进来）。
	if _, ok := d.VerifyAPIKey(ctx, "eyJhbGciOi", ""); ok {
		t.Fatal("非 pk_ 前缀不应通过")
	}
	// last_used_at / last_used_ip 已更新。
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
			if k.LastUsedIP != "10.0.0.9" {
				t.Errorf("last_used_ip 应为 10.0.0.9，实际 %q", k.LastUsedIP)
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
	if _, ok := d.VerifyAPIKey(ctx, raw, ""); ok {
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
	_, raw, err := d.CreateAPIKey(ctx, name, APIKeyScopeRead, &past)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, ok := d.VerifyAPIKey(ctx, raw, ""); ok {
		t.Fatal("已过期的 Key 不应通过")
	}

	future := time.Now().Add(24 * time.Hour)
	_, raw2, err := d.CreateAPIKey(ctx, name+"-ok", APIKeyScopeRead, &future)
	if err != nil {
		t.Fatalf("create ok: %v", err)
	}
	defer d.Exec(`DELETE FROM api_keys WHERE name=$1`, name+"-ok")
	if _, ok := d.VerifyAPIKey(ctx, raw2, ""); !ok {
		t.Fatal("未过期的 Key 应通过")
	}
}

// TestAPIKeyScopeMatrix 锁住三档 scope 的包含关系与保守兜底——这些是纯函数，
// 不依赖数据库，改动授权逻辑时最先要过的一关。
func TestAPIKeyScopeMatrix(t *testing.T) {
	cases := []struct {
		scope, need string
		want        bool
	}{
		{APIKeyScopeRead, APIKeyScopeRead, true},
		{APIKeyScopeRead, APIKeyScopeWrite, false},
		{APIKeyScopeRead, APIKeyScopeAdmin, false},
		{APIKeyScopeWrite, APIKeyScopeRead, true},
		{APIKeyScopeWrite, APIKeyScopeWrite, true},
		{APIKeyScopeWrite, APIKeyScopeAdmin, false},
		{APIKeyScopeAdmin, APIKeyScopeRead, true},
		{APIKeyScopeAdmin, APIKeyScopeWrite, true},
		{APIKeyScopeAdmin, APIKeyScopeAdmin, true},
		// 未知档位一律不通过；未知需求一律按 admin（多拦不漏放）。
		{"", APIKeyScopeRead, false},
		{"bogus", APIKeyScopeAdmin, false},
		{APIKeyScopeAdmin, "bogus", true},
	}
	for _, c := range cases {
		if got := APIKeyScopeAllows(c.scope, c.need); got != c.want {
			t.Errorf("APIKeyScopeAllows(%q,%q)=%v, want %v", c.scope, c.need, got, c.want)
		}
	}
}

func TestNormalizeAPIKeyScope(t *testing.T) {
	for in, want := range map[string]string{
		"":       APIKeyScopeRead,
		" READ ": APIKeyScopeRead,
		"Write":  APIKeyScopeWrite,
		"admin":  APIKeyScopeAdmin,
		"delete": APIKeyScopeRead, // 未知值降到最小权限
		"root":   APIKeyScopeRead,
	} {
		if got := NormalizeAPIKeyScope(in); got != want {
			t.Errorf("NormalizeAPIKeyScope(%q)=%q, want %q", in, got, want)
		}
	}
}
