package db

import (
	"testing"
	"time"
)

// 拓扑排序：上游步骤必须排在下游之前；无关步骤按时间稳定排列；序号连续。
func TestOrderChainSteps(t *testing.T) {
	now := time.Now()
	c := &AttackChain{
		Steps: []*AttackChainStep{
			{FindingID: 30, CreatedAt: now.Add(3 * time.Minute)},
			{FindingID: 10, CreatedAt: now},
			{FindingID: 20, CreatedAt: now.Add(1 * time.Minute)},
		},
		Edges: []FindingChainEdge{
			{Src: 10, Dst: 30, Rel: "enables"},
			{Src: 20, Dst: 30, Rel: "enables"},
		},
	}
	orderChainSteps(c)
	if got := []int64{c.Steps[0].FindingID, c.Steps[1].FindingID, c.Steps[2].FindingID}; got[0] != 10 || got[1] != 20 || got[2] != 30 {
		t.Fatalf("order = %v want [10 20 30]", got)
	}
	for i, s := range c.Steps {
		if s.Seq != i+1 {
			t.Fatalf("step %d seq=%d", i, s.Seq)
		}
	}
}

// 环（理论上不会出现）也不能丢步骤。
func TestOrderChainStepsCycleKeepsAll(t *testing.T) {
	now := time.Now()
	c := &AttackChain{
		Steps: []*AttackChainStep{
			{FindingID: 1, CreatedAt: now},
			{FindingID: 2, CreatedAt: now.Add(time.Minute)},
		},
		Edges: []FindingChainEdge{
			{Src: 1, Dst: 2, Rel: "enables"},
			{Src: 2, Dst: 1, Rel: "enables"},
		},
	}
	orderChainSteps(c)
	if len(c.Steps) != 2 {
		t.Fatalf("dropped steps: %d", len(c.Steps))
	}
}
