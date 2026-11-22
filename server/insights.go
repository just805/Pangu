package server

import "net/http"

// insights 跨任务洞察（路线图 C4）：按天/企业聚合任务、严重度与 token 成本。
// 查询参数：days（默认 30，1..365）、company（可选企业 id）。
func (s *Server) insights(w http.ResponseWriter, r *http.Request) {
	days := atoiDefault(r.URL.Query().Get("days"), 30)
	if days < 1 {
		days = 30
	}
	if days > 365 {
		days = 365
	}
	companyID := int64(atoiDefault(r.URL.Query().Get("company"), 0))
	data, err := s.m.pg.Insights(days, companyID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, data)
}
