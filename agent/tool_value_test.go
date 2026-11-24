package agent

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/just805/pangu/db"
)

func factNode(id int64, confidence, evidence string) *db.Node {
	p := map[string]any{"summary": fmt.Sprintf("fact-%d", id)}
	if confidence != "" {
		p["confidence"] = confidence
	}
	if evidence != "" {
		p["evidence"] = evidence
	}
	raw, _ := json.Marshal(p)
	return &db.Node{ID: id, Payload: raw}
}

func TestFactValueScore(t *testing.T) {
	yieldsOf := map[int64][]int64{7: {100, 101}}
	tokens := map[int64]int64{8: 9_000_000}
	observed := factValueScore(factNode(1, "observed", "HTTP 200 body: admin"), 0, nil, nil)
	inferred := factValueScore(factNode(2, "inferred", ""), 0, nil, nil)
	if observed <= inferred {
		t.Fatalf("observed+evidence (%d) must outrank inferred-no-evidence (%d)", observed, inferred)
	}
	// 由有产出的意图生产的 fact 加分；由昂贵零产出意图生产的减分。
	fromGood := factValueScore(factNode(3, "inferred", ""), 7, yieldsOf, tokens)
	fromWaste := factValueScore(factNode(4, "inferred", ""), 8, yieldsOf, tokens)
	if fromGood <= fromWaste {
		t.Fatalf("fact from productive intent (%d) must outrank one from expensive zero-yield intent (%d)", fromGood, fromWaste)
	}
}

func TestSelectFactsByValueKeepsHighValueOld(t *testing.T) {
	facts := make([]*db.Node, 0, 25)
	for i := 0; i < 24; i++ {
		facts = append(facts, factNode(int64(i), "", "")) // 新→旧，低价值
	}
	facts = append(facts, factNode(24, "observed", "verified command output")) // 最旧但高价值

	got := selectFactsByValue(facts, 20, 8, nil, nil, nil)
	if len(got) != 20 {
		t.Fatalf("want 20 facts after truncation, got %d", len(got))
	}
	ids := map[int64]bool{}
	for _, n := range got {
		ids[n.ID] = true
	}
	if !ids[24] {
		t.Fatalf("high-value old fact (id=24) must survive truncation")
	}
	if ids[23] {
		t.Fatalf("low-value newer fact (id=23) should be dropped to make room")
	}
}

func TestSelectFactsByValueUnderCapKeepsOrder(t *testing.T) {
	facts := []*db.Node{factNode(3, "", ""), factNode(2, "", ""), factNode(1, "observed", "")}
	got := selectFactsByValue(facts, 20, 8, nil, nil, nil)
	if len(got) != len(facts) {
		t.Fatalf("under-cap must keep all, got %d", len(got))
	}
	for i := range facts {
		if got[i].ID != facts[i].ID {
			t.Fatalf("under-cap order changed at %d: got %d want %d", i, got[i].ID, facts[i].ID)
		}
	}
}

func TestSelectIntentsByValuePrefersYields(t *testing.T) {
	intents := make([]*db.Node, 0, 15)
	for i := 0; i < 14; i++ {
		intents = append(intents, &db.Node{ID: int64(i)})
	}
	intents = append(intents, &db.Node{ID: 14}) // 最旧但有产出
	yieldsOf := map[int64][]int64{14: {200}}
	got := selectIntentsByValue(intents, 12, 6, yieldsOf, nil)
	if len(got) != 12 {
		t.Fatalf("want 12 intents, got %d", len(got))
	}
	found := false
	for _, n := range got {
		if n.ID == 14 {
			found = true
		}
	}
	if !found {
		t.Fatalf("intent with yields must survive truncation")
	}
}
