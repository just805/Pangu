package db

// 跨任务洞察（路线图 C4）：管理层汇报层。按企业/时间聚合任务、漏洞严重度分布
// 与 token 成本，给出「单位发现成本」（token / 确认漏洞）。全部只读聚合查询，
// 不新增长期表；口径与报告一致：false_positive / duplicate / ignored 不计入。

import (
	"fmt"
	"sort"
	"time"
)

// InsightSeverityCounts 是严重度分桶计数。
type InsightSeverityCounts struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
	Other    int `json:"other"`
}

func (c *InsightSeverityCounts) add(sev string) {
	switch sev {
	case "critical":
		c.Critical++
	case "high":
		c.High++
	case "medium":
		c.Medium++
	case "low":
		c.Low++
	default:
		c.Other++
	}
}

// Total 返回计入洞察的漏洞总数。
func (c InsightSeverityCounts) Total() int {
	return c.Critical + c.High + c.Medium + c.Low + c.Other
}

// InsightDay 是趋势中的一个自然日。
type InsightDay struct {
	Date     string                `json:"date"`
	Tasks    int                   `json:"tasks"`
	Findings int                   `json:"findings"`
	Severity InsightSeverityCounts `json:"severity"`
}

// InsightCompany 是按企业聚合的一行。
type InsightCompany struct {
	CompanyID        int64                 `json:"company_id"`
	Name             string                `json:"name"`
	Tasks            int                   `json:"tasks"`
	Findings         int                   `json:"findings"`
	Severity         InsightSeverityCounts `json:"severity"`
	Tokens           int64                 `json:"tokens"`
	TokensPerFinding float64               `json:"tokens_per_finding"`
}

// InsightTotals 是所选范围的合计。
type InsightTotals struct {
	Tasks            int                   `json:"tasks"`
	Findings         int                   `json:"findings"`
	Severity         InsightSeverityCounts `json:"severity"`
	Tokens           int64                 `json:"tokens"`
	TokensPerFinding float64               `json:"tokens_per_finding"`
}

// Insights 是洞察页的完整响应。
type Insights struct {
	Days      int              `json:"days"`
	From      string           `json:"from"`
	To        string           `json:"to"`
	Totals    InsightTotals    `json:"totals"`
	Trend     []InsightDay     `json:"trend"`
	Companies []InsightCompany `json:"companies"`
}

// insightExcludeStatuses 与报告导出口径一致：误报/重复/忽略不算有效发现。
const insightExcludeStatuses = "('false_positive','duplicate','ignored')"

// Insights 聚合最近 days 天（companyID>0 时只看该企业）的任务/漏洞/成本。
func (d *DB) Insights(days int, companyID int64) (*Insights, error) {
	if days <= 0 {
		days = 30
	}
	now := time.Now().UTC()
	from := now.AddDate(0, 0, -(days - 1))
	out := &Insights{
		Days:      days,
		From:      from.Format("2006-01-02"),
		To:        now.Format("2006-01-02"),
		Trend:     []InsightDay{},
		Companies: []InsightCompany{},
	}

	companyCond := ""
	args := []any{days}
	if companyID > 0 {
		companyCond = fmt.Sprintf(` AND EXISTS (SELECT 1 FROM task_scope sc
			WHERE sc.task_id=t.id AND sc.kind='company' AND sc.company_id=$%d)`, len(args)+1)
		args = append(args, companyID)
	}

	// ── 趋势：按天 × 严重度 ──
	daySev := map[string]*InsightSeverityCounts{}
	rows, err := d.Query(fmt.Sprintf(`
SELECT to_char(date_trunc('day', f.created_at), 'YYYY-MM-DD') AS day, f.severity, count(*)
FROM findings f
JOIN tasks t ON t.id=f.task_id
WHERE t.deleted_at IS NULL
  AND f.created_at >= now() - make_interval(days => $1::int)
  AND f.status NOT IN %s%s
GROUP BY 1,2`, insightExcludeStatuses, companyCond), args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var day, sev string
		var n int
		if err := rows.Scan(&day, &sev, &n); err != nil {
			rows.Close()
			return nil, err
		}
		if daySev[day] == nil {
			daySev[day] = &InsightSeverityCounts{}
		}
		for i := 0; i < n; i++ {
			daySev[day].add(sev)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	// ── 趋势：按天任务数 ──
	dayTasks := map[string]int{}
	trows, err := d.Query(fmt.Sprintf(`
SELECT to_char(date_trunc('day', t.created_at), 'YYYY-MM-DD'), count(*)
FROM tasks t
WHERE t.deleted_at IS NULL
  AND t.created_at >= now() - make_interval(days => $1::int)%s
GROUP BY 1`, companyCond), args...)
	if err != nil {
		return nil, err
	}
	for trows.Next() {
		var day string
		var n int
		if err := trows.Scan(&day, &n); err != nil {
			trows.Close()
			return nil, err
		}
		dayTasks[day] = n
	}
	if err := trows.Err(); err != nil {
		trows.Close()
		return nil, err
	}
	trows.Close()

	// 补齐无数据的日期，图上看得到连续趋势。
	for i := 0; i < days; i++ {
		day := from.AddDate(0, 0, i).Format("2006-01-02")
		item := InsightDay{Date: day, Tasks: dayTasks[day]}
		if c := daySev[day]; c != nil {
			item.Severity = *c
			item.Findings = c.Total()
		}
		out.Trend = append(out.Trend, item)
		out.Totals.Severity.Critical += item.Severity.Critical
		out.Totals.Severity.High += item.Severity.High
		out.Totals.Severity.Medium += item.Severity.Medium
		out.Totals.Severity.Low += item.Severity.Low
		out.Totals.Severity.Other += item.Severity.Other
		out.Totals.Tasks += item.Tasks
		out.Totals.Findings += item.Findings
	}

	// ── 企业维度：任务 / 漏洞 / 严重度 ──
	crows, err := d.Query(`
SELECT c.id, c.name,
       count(DISTINCT t.id) AS tasks,
       count(f.id) AS findings,
       count(f.id) FILTER (WHERE f.severity='critical') AS critical,
       count(f.id) FILTER (WHERE f.severity='high') AS high,
       count(f.id) FILTER (WHERE f.severity='medium') AS medium,
       count(f.id) FILTER (WHERE f.severity='low') AS low,
       count(f.id) FILTER (WHERE f.severity NOT IN ('critical','high','medium','low')) AS other
FROM companies c
JOIN task_scope sc ON sc.company_id=c.id AND sc.kind='company'
JOIN tasks t ON t.id=sc.task_id AND t.deleted_at IS NULL
            AND t.created_at >= now() - make_interval(days => $1::int)
LEFT JOIN findings f ON f.task_id=t.id
            AND f.created_at >= now() - make_interval(days => $1::int)
            AND f.status NOT IN ('false_positive','duplicate','ignored')
WHERE ($2::bigint = 0 OR c.id=$2)
GROUP BY c.id, c.name
ORDER BY findings DESC, tasks DESC, c.name`, days, companyID)
	if err != nil {
		return nil, err
	}
	for crows.Next() {
		var c InsightCompany
		if err := crows.Scan(&c.CompanyID, &c.Name, &c.Tasks, &c.Findings,
			&c.Severity.Critical, &c.Severity.High, &c.Severity.Medium, &c.Severity.Low, &c.Severity.Other); err != nil {
			crows.Close()
			return nil, err
		}
		out.Companies = append(out.Companies, c)
	}
	if err := crows.Err(); err != nil {
		crows.Close()
		return nil, err
	}
	crows.Close()

	// ── 成本：每任务 token → 按企业汇总（多企业归属时各自计全额，管理视角）──
	tokensByCompany, totalTokens, err := d.insightTokens(days, companyID)
	if err != nil {
		return nil, err
	}
	out.Totals.Tokens = totalTokens
	out.Totals.TokensPerFinding = perFinding(totalTokens, out.Totals.Findings)
	for i := range out.Companies {
		c := &out.Companies[i]
		c.Tokens = tokensByCompany[c.CompanyID]
		c.TokensPerFinding = perFinding(c.Tokens, c.Findings)
	}
	// 找出「有成本但范围内无漏洞/任务」的企业行也不要丢：成本查询可能包含
	// 窗口内创建、但漏洞全部被排除的任务；这里以任务维度补行。
	for id, tok := range tokensByCompany {
		found := false
		for i := range out.Companies {
			if out.Companies[i].CompanyID == id {
				out.Companies[i].Tokens = tok
				out.Companies[i].TokensPerFinding = perFinding(tok, out.Companies[i].Findings)
				found = true
				break
			}
		}
		if found {
			continue
		}
		var name string
		if err := d.QueryRow(`SELECT name FROM companies WHERE id=$1`, id).Scan(&name); err != nil {
			name = fmt.Sprintf("企业 #%d", id)
		}
		out.Companies = append(out.Companies, InsightCompany{CompanyID: id, Name: name, Tokens: tok})
	}
	sort.SliceStable(out.Companies, func(i, j int) bool {
		if out.Companies[i].Findings != out.Companies[j].Findings {
			return out.Companies[i].Findings > out.Companies[j].Findings
		}
		return out.Companies[i].Tokens > out.Companies[j].Tokens
	})
	return out, nil
}

func perFinding(tokens int64, findings int) float64 {
	if findings <= 0 {
		return 0
	}
	return float64(tokens) / float64(findings)
}

// insightTokens 返回 企业 id → token 合计，以及范围内总 token。
func (d *DB) insightTokens(days int, companyID int64) (map[int64]int64, int64, error) {
	var total int64
	if err := d.QueryRow(`
SELECT COALESCE(SUM(u.input_tokens+u.output_tokens+u.cache_read+u.cache_write),0)
FROM llm_usage u
JOIN tasks t ON u.task_id = t.id::text
WHERE t.deleted_at IS NULL
  AND u.ts >= now() - make_interval(days => $1::int)
  AND ($2::bigint = 0 OR EXISTS (SELECT 1 FROM task_scope sc
        WHERE sc.task_id=t.id AND sc.kind='company' AND sc.company_id=$2))`,
		days, companyID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := d.Query(`
WITH task_tokens AS (
  SELECT t.id, COALESCE(SUM(u.input_tokens+u.output_tokens+u.cache_read+u.cache_write),0) AS tok
  FROM tasks t
  LEFT JOIN llm_usage u ON u.task_id = t.id::text AND u.ts >= now() - make_interval(days => $1::int)
  WHERE t.deleted_at IS NULL
    AND t.created_at >= now() - make_interval(days => $1::int)
  GROUP BY t.id
)
SELECT sc.company_id, SUM(tt.tok)
FROM task_tokens tt
JOIN task_scope sc ON sc.task_id=tt.id AND sc.kind='company'
WHERE ($2::bigint = 0 OR sc.company_id=$2)
GROUP BY sc.company_id`, days, companyID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var id, tok int64
		if err := rows.Scan(&id, &tok); err != nil {
			return nil, 0, err
		}
		out[id] = tok
	}
	return out, total, rows.Err()
}
