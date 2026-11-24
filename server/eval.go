package server

import (
	"net/http"
	"strconv"
	"strings"
)

// benchmark 暴露评测基线聚合（成功率 / 误报率 / 未验证率 / 成本 / 时长）。
// 只读，供发版对比与 C5 决策使用：?days=N（默认 90）；?task_ids=1,2,3 固定一组
// 标定任务（给定时忽略 days，便于跨版本复现对比）。
func (s *Server) benchmark(w http.ResponseWriter, r *http.Request) {
	days := atoiDefault(r.URL.Query().Get("days"), 90)
	var taskIDs []int64
	for _, part := range strings.Split(r.URL.Query().Get("task_ids"), ",") {
		if id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64); err == nil && id > 0 {
			taskIDs = append(taskIDs, id)
		}
	}
	data, err := s.m.pg.BenchmarkSummary(r.Context(), days, taskIDs)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, data)
}
