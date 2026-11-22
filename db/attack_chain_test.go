package db

import (
	"context"
	"testing"
)

// 攻击链推导：显式前置（finding→intent 的 derived_from）、血缘推进（enables）、
// 同源并报（同一意图的多条 finding）三条关系都要成立，且步骤拓扑有序。
func TestAttackChainDerivation(t *testing.T) {
	dsn, _, err := DSN()
	if err != nil || dsn == "" {
		t.Skip("requires PANGU_PG_DSN")
	}
	d, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ctx := context.Background()

	task, err := d.CreateTask("attack chain test", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	es := d.Exploration(task.ExplorationID)

	intent1, err := es.AddIntent(map[string]any{"summary": "拿到数据库凭据"}, 5, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	fact1, err := es.AddNode(KindFact, map[string]any{"summary": "在 .env 中找到数据库口令"}, 0, "confirmed", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := es.Link(intent1, RelYields, fact1); err != nil {
		t.Fatal(err)
	}
	intent2, err := es.AddIntent(map[string]any{"summary": "用窃取的口令登录后台并执行命令"}, 5, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	// 显式前置：finding1 节点 → intent2。
	// 同时 fact1 → intent2 让 intent1 成为 intent2 的血缘祖先（enables 路径）。
	if err := es.Link(fact1, RelDerivedFrom, intent2); err != nil {
		t.Fatal(err)
	}

	base := RecordFindingInput{
		TaskID:        task.ID,
		ExplorationID: task.ExplorationID,
		VulnClass:     "credential_leak",
		Severity:      "high",
		Summary:       "env 泄露",
		Evidence:      "GET /.env => 200 DB_PASSWORD=...",
		Worker:        "work#1",
	}
	in1 := base
	in1.Name = ".env 泄露数据库凭据"
	in1.IntentID = intent1
	f1, err := es.RecordFinding(ctx, in1)
	if err != nil {
		t.Fatal(err)
	}

	in3 := base
	in3.Name = "同源日志泄露内部令牌"
	in3.VulnClass = "sensitive_exposure"
	in3.Severity = "medium"
	in3.IntentID = intent1
	f3, err := es.RecordFinding(ctx, in3)
	if err != nil {
		t.Fatal(err)
	}

	in2 := base
	in2.Name = "后台命令执行"
	in2.VulnClass = "rce"
	in2.Severity = "critical"
	in2.Evidence = "POST /admin/exec => 200 uid=0(root)"
	in2.IntentID = intent2
	// finding1 → intent2 的显式前置（模拟 planner 用 finding 作为 parent_ids）。
	if err := es.Link(f1.NodeID, RelDerivedFrom, intent2); err != nil {
		t.Fatal(err)
	}
	f2, err := es.RecordFinding(ctx, in2)
	if err != nil {
		t.Fatal(err)
	}

	chain, err := es.AttackChain()
	if err != nil {
		t.Fatal(err)
	}
	if len(chain.Steps) != 3 {
		t.Fatalf("steps=%d want 3", len(chain.Steps))
	}
	has := func(src, dst int64, rel string) bool {
		for _, e := range chain.Edges {
			if e.Src == src && e.Dst == dst && e.Rel == rel {
				return true
			}
		}
		return false
	}
	if !has(f1.FindingID, f2.FindingID, "derived_from") {
		t.Fatalf("missing derived_from f1→f2; edges=%+v", chain.Edges)
	}
	if !has(f3.FindingID, f2.FindingID, "enables") {
		t.Fatalf("missing enables f3→f2; edges=%+v", chain.Edges)
	}
	if !has(f1.FindingID, f3.FindingID, "chained_with") {
		t.Fatalf("missing chained_with f1→f3; edges=%+v", chain.Edges)
	}
	// f2 必须排在最后：两条上游都指向它。
	if chain.Steps[len(chain.Steps)-1].FindingID != f2.FindingID {
		t.Fatalf("last step=%d want f2=%d (steps=%+v)", chain.Steps[len(chain.Steps)-1].FindingID, f2.FindingID, chain.Steps)
	}
	for i, s := range chain.Steps {
		if s.Seq != i+1 {
			t.Fatalf("step %d seq=%d", i, s.Seq)
		}
		if s.IntentID == 0 {
			t.Fatalf("step %d missing intent", i)
		}
	}
	for _, s := range chain.Steps {
		if s.FindingID == f2.FindingID && len(s.Proofs) == 0 {
			t.Fatal("f2 should carry proofs (intent/evidence)")
		}
	}
}

// 没有 finding 时攻击链返回空，不报错、不写边。
func TestAttackChainEmpty(t *testing.T) {
	dsn, _, err := DSN()
	if err != nil || dsn == "" {
		t.Skip("requires PANGU_PG_DSN")
	}
	d, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })

	task, err := d.CreateTask("empty chain test", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })

	chain, err := d.Exploration(task.ExplorationID).AttackChain()
	if err != nil {
		t.Fatal(err)
	}
	if len(chain.Steps) != 0 || len(chain.Edges) != 0 {
		t.Fatalf("empty chain: steps=%d edges=%d", len(chain.Steps), len(chain.Edges))
	}
}
