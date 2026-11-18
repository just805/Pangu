package server

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"strings"

	"github.com/just805/pangu/db"
)

// ── 跨任务经验沉淀（P1）──────────────────────────────────────────────────────
//
// 任务收官时规则抽取「有效手法 / 受限教训」写回 task_lessons；新任务的 planner
// 态势按企业交集注入（见 task_llm.go 的 crossLessons 闭包）。规则式抽取零 LLM
// 成本、确定性可测；质量上限有限但作为跨任务的「已知死路/有效打法」索引足够。

// collectTaskLessons 在任务终态落定后抽取本任务经验（异步，不阻塞收官路径）。
//   - technique：done 意图的有效手法（最近 3 条 summary）；
//   - lesson：blocked 意图的受限教训（最近 3 条 summary + stop_reason）。
//
// exhausted 不取：手段穷尽是中性结论，无手法价值；零写回的 exhausted 已由 B2
// 复盘机制处理。同一任务重复收官幂等重写（ReplaceTaskLessons 先删后插）。
func (e *Engine) collectTaskLessons(t *Task) {
	if t == nil || t.Store == nil || e.m == nil || e.m.pg == nil {
		return
	}
	taskNum, err := strconv.ParseInt(t.ID, 10, 64)
	if err != nil || taskNum <= 0 {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[lessons] task %s 经验抽取 panic: %v", t.ID, r)
			}
		}()
		ctx := context.Background()
		intents, err := t.Store.ListByKind(db.KindIntent, 500)
		if err != nil {
			log.Printf("[lessons] task %s 读取意图失败: %v", t.ID, err)
			return
		}
		var techniques, lessons []db.TaskLesson
		for _, n := range intents {
			var p map[string]any
			if len(n.Payload) > 0 {
				_ = json.Unmarshal(n.Payload, &p)
			}
			summary := ""
			if p != nil {
				if s, ok := p["summary"].(string); ok {
					summary = strings.TrimSpace(s)
				}
			}
			if summary == "" {
				continue
			}
			summary = truncateRunes(summary, 120)
			switch n.State {
			case "done":
				if len(techniques) < 3 {
					techniques = append(techniques, db.TaskLesson{
						Kind: "technique", Text: "有效手法（已完成方向）：" + summary, SourceIntentID: n.ID,
					})
				}
			case "blocked":
				if len(lessons) < 3 {
					text := "受限教训（被拦方向）：" + summary
					if n.BlockedReason != "" {
						text += "（原因：" + truncateRunes(n.BlockedReason, 100) + "）"
					}
					lessons = append(lessons, db.TaskLesson{Kind: "lesson", Text: text, SourceIntentID: n.ID})
				}
			}
			if len(techniques) >= 3 && len(lessons) >= 3 {
				break
			}
		}
		all := append(techniques, lessons...)
		if len(all) == 0 {
			return
		}
		companyIDs, err := e.m.pg.TaskCompanyIDs(taskNum)
		if err != nil {
			log.Printf("[lessons] task %s 读企业归属失败: %v", t.ID, err)
			return
		}
		if err := e.m.pg.ReplaceTaskLessons(ctx, taskNum, companyIDs, all); err != nil {
			log.Printf("[lessons] task %s 经验落库失败: %v", t.ID, err)
			return
		}
		log.Printf("[lessons] task %s 沉淀 %d 条经验（手法 %d / 教训 %d，企业 %v）",
			t.ID, len(all), len(techniques), len(lessons), companyIDs)
	}()
}
