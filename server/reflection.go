package server

// 意图终态经验写入（上游 0.4.100 P0-2 的确定性版本）。
//
// 每个意图到达终态时，把「维度 / 终态 / 停止原因 / 产出数 / 摘要」写一条到
// intent_reflections，不调用 LLM、不增加成本。planner 每轮、worker 启动时读它，
// 避免同一种失败打法在新意图上重复烧 token（线上基线 done 18 : exhausted 99 :
// blocked 82，绝大多数方向是白烧）。

import (
	"encoding/json"
	"log"

	"github.com/just805/pangu/agent"
	"github.com/just805/pangu/db"
)

// recordIntentReflection 在意图终态落库后写一条经验；失败只记日志，不影响终态。
func (e *Engine) recordIntentReflection(t *Task, intent *db.Node, state, stopReason string) {
	if e == nil || t == nil || t.Store == nil || intent == nil || state == "" {
		return
	}
	var p struct {
		Summary  string          `json:"summary"`
		AssetIDs json.RawMessage `json:"asset_ids"`
	}
	_ = json.Unmarshal(intent.Payload, &p)
	yields := 0
	if ids, err := t.Store.FactsYielded(intent.ID); err == nil {
		yields = len(ids)
	}
	rec := db.IntentReflection{
		IntentID:      intent.ID,
		Dimension:     agent.DimensionFromPayload(intent.Payload),
		TerminalState: state,
		StopReason:    truncateStopReason(stopReason),
		Summary:       truncateRunes(p.Summary, 240),
		Yields:        yields,
		Assets:        intentAssetIDs(p.AssetIDs),
	}
	if err := t.Store.RecordIntentReflection(rec); err != nil {
		log.Printf("[reflection] task %s intent %d 写经验失败: %v", t.ID, intent.ID, err)
	}
}

// intentAssetIDs 从意图 payload 的 asset_ids 取资产列表（兼容 target_ids/target_id 旧键）。
func intentAssetIDs(raw json.RawMessage) []int64 {
	if len(raw) == 0 {
		return nil
	}
	var ids []int64
	if json.Unmarshal(raw, &ids) == nil {
		return ids
	}
	var one int64
	if json.Unmarshal(raw, &one) == nil && one > 0 {
		return []int64{one}
	}
	return nil
}
