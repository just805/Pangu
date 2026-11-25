package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/just805/pangu/db"
)

// ═══ M-14：靶场判卷 HTTP 面 ═══
//
// 与既有 GET /api/eval/benchmark（真实任务聚合）区分：
// 这里是「标准答案 + 判卷」，用于验证 Agent 侧改动有没有负优化。

func (s *Server) listEvalBenchmarks(w http.ResponseWriter, r *http.Request) {
	pg := s.m.PG()
	if pg == nil {
		writeJSON(w, 200, []any{})
		return
	}
	list, err := pg.EvalBenchmarks().List(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, list)
}

func (s *Server) createEvalBenchmark(w http.ResponseWriter, r *http.Request) {
	pg := s.m.PG()
	if pg == nil {
		writeErr(w, 503, "需要 PostgreSQL")
		return
	}
	var body struct {
		Name        string                `json:"name"`
		Description string                `json:"description"`
		Expected    []db.ExpectedFinding  `json:"expected"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeErr(w, 400, "name 必填")
		return
	}
	if len(body.Expected) == 0 {
		writeErr(w, 400, "expected 不能为空：至少要有一条标准答案")
		return
	}
	id, err := pg.EvalBenchmarks().Create(r.Context(), body.Name, body.Description, body.Expected)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"id": id})
}

func (s *Server) deleteEvalBenchmark(w http.ResponseWriter, r *http.Request) {
	pg := s.m.PG()
	if pg == nil {
		writeErr(w, 503, "需要 PostgreSQL")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "非法的标准答案 id")
		return
	}
	if err := pg.EvalBenchmarks().Delete(r.Context(), id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": 1})
}

// scoreEvalBenchmark 对某个任务按标准答案判卷。
// ?task=<id> 必填——判卷对象是「跑过靶场的那个任务」。
func (s *Server) scoreEvalBenchmark(w http.ResponseWriter, r *http.Request) {
	pg := s.m.PG()
	if pg == nil {
		writeErr(w, 503, "需要 PostgreSQL")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "非法的标准答案 id")
		return
	}
	taskID := strings.TrimSpace(r.URL.Query().Get("task"))
	if taskID == "" {
		writeErr(w, 400, "缺少 ?task=<任务 id>：判卷对象是跑过靶场的那个任务")
		return
	}
	b, err := pg.EvalBenchmarks().Get(r.Context(), id)
	if err != nil || b == nil {
		writeErr(w, 404, "标准答案不存在")
		return
	}
	findings, _, err := pg.ListFindingsPage(db.FindingFilter{TaskID: taskID}, 1, 1000)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	names := s.reportAssetNames(findings)
	actual := make([]db.ActualFinding, 0, len(findings))
	for _, f := range findings {
		labels := make([]string, 0, len(f.AssetIDs))
		for _, aid := range f.AssetIDs {
			if n := strings.TrimSpace(names[aid]); n != "" {
				labels = append(labels, n)
			}
		}
		actual = append(actual, db.ActualFinding{
			FindingID: f.ID, VulnClass: f.VulnClass, Name: f.Name, AssetLabels: labels,
		})
	}
	score := db.ScoreBenchmark(b.Expected, actual)
	writeJSON(w, 200, map[string]any{
		"benchmark": map[string]any{"id": b.ID, "name": b.Name},
		"task_id":   taskID,
		"score":     score,
	})
}
