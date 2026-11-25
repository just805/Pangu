package db

import (
	"context"
	"testing"
)

// 矩阵按 task_id 归属，测试里用一个不会与真实任务冲突的高位 id，结束后清理。
func TestAuthzMatrixUpsertListStats(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	defer d.Close()
	const taskID = int64(990001)
	const expID = int64(990001)
	ctx := context.Background()
	store := d.AuthzMatrix()
	defer store.DeleteByTask(ctx, taskID)

	// 首次写入。
	id1, err := store.Upsert(ctx, taskID, expID, AuthzEntry{
		Role: "user-a", Host: "api.example.com", Method: "GET", Path: "/api/orders/1",
		Expected: "deny", Observed: "allow", StatusCode: 200, TrafficID: "t-1",
		Note: "A 读到 B 的订单",
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if id1 <= 0 {
		t.Fatal("应返回行 id")
	}
	// 同键覆盖：不新增行，只更新观测。
	id2, err := store.Upsert(ctx, taskID, expID, AuthzEntry{
		Role: "user-a", Host: "api.example.com", Method: "GET", Path: "/api/orders/1",
		Expected: "deny", Observed: "deny", StatusCode: 403, TrafficID: "t-2",
	})
	if err != nil {
		t.Fatalf("upsert overwrite: %v", err)
	}
	if id2 != id1 {
		t.Fatalf("同键应覆盖同一行: %d != %d", id1, id2)
	}
	// 第二条：未测 + 无证据。
	if _, err := store.Upsert(ctx, taskID, expID, AuthzEntry{
		Role: "user-b", Method: "POST", Path: "/api/orders",
		Expected: "allow", Observed: "unknown",
	}); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}

	entries, err := store.List(taskID, "", 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("应有 2 条，实际 %d", len(entries))
	}
	// 按角色过滤。
	onlyB, err := store.List(taskID, "user-b", 0)
	if err != nil || len(onlyB) != 1 {
		t.Fatalf("角色过滤失败: %v len=%d", err, len(onlyB))
	}

	st, err := store.Stats(taskID)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if st.Total != 2 || st.Allowed != 0 || st.Denied != 1 || st.Unknown != 1 {
		t.Errorf("统计不符: %+v", st)
	}
	// 第一条已改成 deny，与 expected 一致 → 不再 suspicious。
	if st.Suspicious != 0 {
		t.Errorf("suspicious 应为 0，实际 %d", st.Suspicious)
	}
	// 第二条 unknown 不计入 no_evidence；第一条有 t-2。
	if st.NoEvidence != 0 {
		t.Errorf("no_evidence 应为 0，实际 %d", st.NoEvidence)
	}
}

func TestAuthzMatrixRequiresContext(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	defer d.Close()
	if _, err := d.AuthzMatrix().Upsert(context.Background(), 0, 0, AuthzEntry{Role: "r", Path: "/x"}); err == nil {
		t.Fatal("缺任务/探索图上下文应报错")
	}
}

func TestAuthzMatrixSuspiciousAndNoEvidence(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	defer d.Close()
	const taskID = int64(990002)
	ctx := context.Background()
	store := d.AuthzMatrix()
	defer store.DeleteByTask(ctx, taskID)

	rows := []AuthzEntry{
		// expected=deny 但实测 allow：越权线索，且没有流量证据。
		{Role: "user-a", Method: "GET", Path: "/api/a", Expected: "deny", Observed: "allow", StatusCode: 200},
		// expected=allow 实测 allow：正常。
		{Role: "user-a", Method: "GET", Path: "/api/b", Expected: "allow", Observed: "allow", StatusCode: 200, TrafficID: "t-9"},
	}
	for _, r := range rows {
		if _, err := store.Upsert(ctx, taskID, taskID, r); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}
	st, err := store.Stats(taskID)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if st.Suspicious != 1 {
		t.Errorf("suspicious 应为 1，实际 %d", st.Suspicious)
	}
	if st.NoEvidence != 1 {
		t.Errorf("no_evidence 应为 1，实际 %d", st.NoEvidence)
	}
}
