package db

import (
	"context"
	"time"
)

// 评测基线（Agent 能力度量的最小闭环）。
//
// 目标：用库内已有数据回答四个问题——任务成功率、漏洞误报率、证据未验证率、
// 单任务成本/时长。没有这套度量，任何「加强 Agent」都无法证明有效，也无法判断
// 路线图 C5（出报告前自验证）该做多强。全部是只读聚合，不改变任务行为。
//
// 口径：
//   - 成功率：任务终态 status=done（existing settle 只在目标全部达成时落 done）；
//   - 误报率：findings.status='false_positive' / 任务漏洞总数（合并重复不计）；
//   - 未验证率：high/critical 中「证据文本为空且未绑定任何流量」的占比——
//     这是报告阶段最危险的误报来源；
//   - 成本：activity(kind=result) 的 input+output token；
//   - 时长：first_run_at → completed_at（未完成用 now），无 first_run_at 记 0 不计均值。

// BenchmarkTask 是单个任务的评测行。
type BenchmarkTask struct {
	TaskID           int64  `json:"task_id"`
	Name             string `json:"name"`
	Status           string `json:"status"`
	GoalsTotal       int    `json:"goals_total"`
	GoalsMet         int    `json:"goals_met"`
	Success          bool   `json:"success"`
	Findings         int    `json:"findings"`
	Critical         int    `json:"critical"`
	High             int    `json:"high"`
	Medium           int    `json:"medium"`
	Low              int    `json:"low"`
	FalsePositives   int    `json:"false_positives"`
	Confirmed        int    `json:"confirmed"`
	UnverifiedHigh   int    `json:"unverified_high"`
	RetestReproduced int    `json:"retest_reproduced"`
	RetestFixed      int    `json:"retest_fixed"`
	Tokens           int64  `json:"tokens"`
	DurationMinutes  int64  `json:"duration_minutes"`
}

// BenchmarkSummary 是任务集合的聚合结果。
type BenchmarkSummary struct {
	Days              int             `json:"days"`
	From              string          `json:"from"`
	To                string          `json:"to"`
	TaskCount         int             `json:"task_count"`
	SuccessCount      int             `json:"success_count"`
	SuccessRate       float64         `json:"success_rate"`
	Findings          int             `json:"findings"`
	FalsePositives    int             `json:"false_positives"`
	FalsePositiveRate float64         `json:"false_positive_rate"`
	UnverifiedHigh    int             `json:"unverified_high"`
	UnverifiedRate    float64         `json:"unverified_rate"`
	TotalTokens       int64           `json:"total_tokens"`
	AvgTokens         float64         `json:"avg_tokens"`
	AvgDurationMin    float64         `json:"avg_duration_minutes"`
	Tasks             []BenchmarkTask `json:"tasks"`
}

type findingBenchCounts struct {
	fp, confirmed, unverifiedHigh int
}

// BenchmarkSummary 聚合最近 days 天内创建的任务（默认 90）。taskIDs 非空时只统计
// 这些任务（不受 days 限制）——固定一组标定任务即可跨版本复现对比。只读。
func (d *DB) BenchmarkSummary(ctx context.Context, days int, taskIDs []int64) (*BenchmarkSummary, error) {
	if days <= 0 {
		days = 90
	}
	if days > 3650 {
		days = 3650
	}
	now := time.Now().UTC()
	from := now.AddDate(0, 0, -(days - 1))
	var idSet map[int64]bool
	if len(taskIDs) > 0 {
		idSet = make(map[int64]bool, len(taskIDs))
		for _, id := range taskIDs {
			if id > 0 {
				idSet[id] = true
			}
		}
	}

	tasks, err := d.ListTasks()
	if err != nil {
		return nil, err
	}
	selected := make([]*Task, 0, len(tasks))
	ids := make([]int64, 0, len(tasks))
	for _, t := range tasks {
		if idSet != nil {
			if !idSet[t.ID] {
				continue
			}
		} else if t.CreatedAt.Before(from) {
			continue
		}
		selected = append(selected, t)
		ids = append(ids, t.ID)
	}
	metrics, err := d.TaskListMetricsAll()
	if err != nil {
		return nil, err
	}
	findings := map[int64]findingBenchCounts{}
	retests := map[int64]map[string]int{}
	if len(ids) > 0 {
		frows, err := d.QueryContext(ctx, `
SELECT f.task_id,
       COUNT(*) FILTER (WHERE f.status='false_positive'),
       COUNT(*) FILTER (WHERE f.status='confirmed'),
       COUNT(*) FILTER (WHERE f.severity IN ('critical','high')
                         AND COALESCE(NULLIF(btrim(f.evidence),''),'')=''
                         AND NOT EXISTS (SELECT 1 FROM finding_traffic_bindings b WHERE b.finding_id=f.id))
FROM findings f
WHERE f.task_id = ANY($1) AND f.merged_into_id IS NULL
GROUP BY f.task_id`, ids)
		if err != nil {
			return nil, err
		}
		for frows.Next() {
			var tid int64
			var c findingBenchCounts
			if err := frows.Scan(&tid, &c.fp, &c.confirmed, &c.unverifiedHigh); err != nil {
				frows.Close()
				return nil, err
			}
			findings[tid] = c
		}
		if err := frows.Err(); err != nil {
			frows.Close()
			return nil, err
		}
		frows.Close()

		rrows, err := d.QueryContext(ctx, `
SELECT f.task_id, r.verdict, COUNT(*)
FROM finding_retests r
JOIN findings f ON f.id = r.finding_id
WHERE f.task_id = ANY($1) AND r.verdict <> ''
GROUP BY f.task_id, r.verdict`, ids)
		if err != nil {
			return nil, err
		}
		for rrows.Next() {
			var tid int64
			var verdict string
			var n int
			if err := rrows.Scan(&tid, &verdict, &n); err != nil {
				rrows.Close()
				return nil, err
			}
			if retests[tid] == nil {
				retests[tid] = map[string]int{}
			}
			retests[tid][verdict] = n
		}
		if err := rrows.Err(); err != nil {
			rrows.Close()
			return nil, err
		}
		rrows.Close()
	}

	rows := make([]BenchmarkTask, 0, len(selected))
	for _, t := range selected {
		m := metrics[t.ExplorationID]
		fc := findings[t.ID]
		rt := retests[t.ID]
		row := BenchmarkTask{
			TaskID:           t.ID,
			Name:             t.Name,
			Status:           t.Status,
			GoalsTotal:       m.Goals.Total,
			GoalsMet:         m.Goals.Met,
			Success:          t.Status == "done",
			Findings:         m.Findings.Critical + m.Findings.High + m.Findings.Medium + m.Findings.Low,
			Critical:         m.Findings.Critical,
			High:             m.Findings.High,
			Medium:           m.Findings.Medium,
			Low:              m.Findings.Low,
			FalsePositives:   fc.fp,
			Confirmed:        fc.confirmed,
			UnverifiedHigh:   fc.unverifiedHigh,
			RetestReproduced: rt["reproduced"],
			RetestFixed:      rt["fixed"],
			Tokens:           int64(m.Tokens.InputTokens) + int64(m.Tokens.OutputTokens),
			DurationMinutes:  benchmarkDurationMinutes(t, now),
		}
		rows = append(rows, row)
	}

	sum := aggregateBenchmark(rows)
	sum.Days = days
	sum.From = from.Format("2006-01-02")
	sum.To = now.Format("2006-01-02")
	return &sum, nil
}

// benchmarkDurationMinutes 计算任务墙钟时长（分钟）。未运行过返回 0。
func benchmarkDurationMinutes(t *Task, now time.Time) int64 {
	if t.FirstRunAt == nil {
		return 0
	}
	end := now
	if t.CompletedAt != nil {
		end = *t.CompletedAt
	}
	if end.Before(*t.FirstRunAt) {
		return 0
	}
	return int64(end.Sub(*t.FirstRunAt).Minutes())
}

// aggregateBenchmark 是纯聚合函数（便于单测）：从逐任务行算总量与比率。
func aggregateBenchmark(rows []BenchmarkTask) BenchmarkSummary {
	var out BenchmarkSummary
	out.Tasks = rows
	out.TaskCount = len(rows)
	var durSum int64
	var durCount int
	for i := range rows {
		r := &rows[i]
		if r.Success {
			out.SuccessCount++
		}
		out.Findings += r.Findings
		out.FalsePositives += r.FalsePositives
		out.UnverifiedHigh += r.UnverifiedHigh
		out.TotalTokens += r.Tokens
		if r.DurationMinutes > 0 {
			durSum += r.DurationMinutes
			durCount++
		}
	}
	if out.TaskCount > 0 {
		out.SuccessRate = round4(float64(out.SuccessCount) / float64(out.TaskCount))
		out.AvgTokens = round4(float64(out.TotalTokens) / float64(out.TaskCount))
	}
	if out.Findings > 0 {
		out.FalsePositiveRate = round4(float64(out.FalsePositives) / float64(out.Findings))
		out.UnverifiedRate = round4(float64(out.UnverifiedHigh) / float64(out.Findings))
	}
	if durCount > 0 {
		out.AvgDurationMin = round4(float64(durSum) / float64(durCount))
	}
	return out
}

func round4(v float64) float64 {
	return float64(int64(v*10000+0.5)) / 10000
}
