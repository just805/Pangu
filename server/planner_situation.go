package server

// planner-situation：planner 态势的结构化只读视图（批 2 态势卡片化的数据源）。
//
// 此前 planner 的态势（意图终态分布/方向产出率/主要发现）以几千字长文本流在
// 会话消息里，与覆盖图页签割裂。这里从 DB 直接聚合（不依赖 agent 内部注入
// 函数），前端在 planner 会话头部渲染成结构化卡片，30s 轮询刷新。

import (
	"encoding/json"
	"net/http"
	"strconv"
)

type plannerSituationState struct {
	State string `json:"state"`
	Count int    `json:"count"`
}

type plannerSituationDirection struct {
	IntentID int64  `json:"intent_id"`
	State    string `json:"state"`
	Summary  string `json:"summary"`
	Tokens   int64  `json:"tokens"`
}

type plannerSituationSeverity struct {
	Severity string `json:"severity"`
	Count    int    `json:"count"`
}

type plannerSituationActive struct {
	IntentID int64  `json:"intent_id"`
	State    string `json:"state"`
	Summary  string `json:"summary"`
}

type plannerSituation struct {
	States     []plannerSituationState     `json:"states"`
	Directions []plannerSituationDirection `json:"directions"`
	Severities []plannerSituationSeverity  `json:"severities"`
	Actives    []plannerSituationActive    `json:"actives"`
	Findings   int                         `json:"findings"`
}

// intentSummaryText 意图 summary 列存的是 JSON（如 {"summary":"..."}）——尝试
// 解出纯文本；失败回退原文。
func intentSummaryText(raw string) string {
	if raw == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err == nil {
		if s, ok := m["summary"].(string); ok && s != "" {
			return s
		}
	}
	return raw
}

func (s *Server) plannerSituation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t := s.m.ResolveTask(id)
	if t == nil {
		writeErr(w, 404, "task not found")
		return
	}
	expID := t.ExpID
	if expID <= 0 {
		writeJSON(w, 200, plannerSituation{})
		return
	}
	out := plannerSituation{}

	// 意图终态分布（deleted 是用户假删除，不算进态势）。
	rows, err := s.m.pg.Query(`SELECT state, count(*) FROM exploration_nodes
WHERE exploration_id=$1 AND kind='intent' AND state<>'deleted' GROUP BY state ORDER BY count(*) DESC`, expID)
	if err == nil {
		for rows.Next() {
			var st plannerSituationState
			if rows.Scan(&st.State, &st.Count) == nil {
				out.States = append(out.States, st)
			}
		}
		rows.Close()
	}

	// 方向产出率 top 5：终态意图按 token 消耗降序（对标 agent 侧 direction_report
	// 口径——token 高产出低的非 done 方向不值得续派）。
	drows, err := s.m.pg.Query(`SELECT n.id, n.state, COALESCE(n.summary,''),
COALESCE(sum(a.input_tokens + a.output_tokens), 0) AS tokens
FROM exploration_nodes n
LEFT JOIN activity a ON a.node_id = n.id
WHERE n.exploration_id=$1 AND n.kind='intent' AND n.state IN ('done','blocked','exhausted')
GROUP BY n.id, n.state, n.summary
ORDER BY tokens DESC LIMIT 5`, expID)
	if err == nil {
		for drows.Next() {
			var d plannerSituationDirection
			if err := drows.Scan(&d.IntentID, &d.State, &d.Summary, &d.Tokens); err == nil {
				d.Summary = intentSummaryText(d.Summary)
				out.Directions = append(out.Directions, d)
			}
		}
		drows.Close()
	}

	// 交付侧：本任务确认漏洞按严重度计数（分拣剔除口径与报告一致）。
	taskID, _ := strconv.ParseInt(t.ID, 10, 64)
	if taskID > 0 {
		frows, err := s.m.pg.Query(`SELECT severity, count(*) FROM findings
WHERE task_id=$1 AND status NOT IN ('false_positive','duplicate','ignored','risk_accepted')
GROUP BY severity ORDER BY count(*) DESC`, taskID)
		if err == nil {
			for frows.Next() {
				var sv plannerSituationSeverity
				if frows.Scan(&sv.Severity, &sv.Count) == nil {
					out.Severities = append(out.Severities, sv)
				}
			}
			frows.Close()
		}
		if err := s.m.pg.QueryRow(`SELECT count(*) FROM findings
WHERE task_id=$1 AND status NOT IN ('false_positive','duplicate','ignored','risk_accepted')`, taskID).Scan(&out.Findings); err != nil {
			out.Findings = 0
		}
	}

	// 活跃意图（running/open/paused）：planner 眼里「正在跑什么」。
	arows, err := s.m.pg.Query(`SELECT id, state, COALESCE(summary,'') FROM exploration_nodes
WHERE exploration_id=$1 AND kind='intent' AND state IN ('running','open','paused')
ORDER BY id DESC LIMIT 8`, expID)
	if err == nil {
		for arows.Next() {
			var a plannerSituationActive
			if arows.Scan(&a.IntentID, &a.State, &a.Summary) == nil {
				a.Summary = intentSummaryText(a.Summary)
				out.Actives = append(out.Actives, a)
			}
		}
		arows.Close()
	}

	if out.States == nil {
		out.States = []plannerSituationState{}
	}
	if out.Directions == nil {
		out.Directions = []plannerSituationDirection{}
	}
	if out.Severities == nil {
		out.Severities = []plannerSituationSeverity{}
	}
	if out.Actives == nil {
		out.Actives = []plannerSituationActive{}
	}
	writeJSON(w, 200, out)
}
