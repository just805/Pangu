package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/just805/pangu/db"
)

// ═══ M-10：报告模板 HTTP 面 ═══
//
// 模板只影响交付件渲染（页眉/密级/委托方覆盖/章节开关），不改数据。
// 没有默认模板时导出行为与历史完全一致。

func (s *Server) listReportTemplates(w http.ResponseWriter, r *http.Request) {
	pg := s.m.PG()
	if pg == nil {
		writeJSON(w, 200, []any{})
		return
	}
	list, err := pg.ReportTemplates().List(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, list)
}

func (s *Server) createReportTemplate(w http.ResponseWriter, r *http.Request) {
	pg := s.m.PG()
	if pg == nil {
		writeErr(w, 503, "需要 PostgreSQL")
		return
	}
	var tpl db.ReportTemplate
	if err := json.NewDecoder(r.Body).Decode(&tpl); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if strings.TrimSpace(tpl.Name) == "" {
		writeErr(w, 400, "name 必填")
		return
	}
	tpl.ID = 0
	tpl.IsDefault = false // 默认由 SetDefault 单独切换，避免多默认并发写入
	id, err := pg.ReportTemplates().Create(r.Context(), tpl)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"id": id})
}

func (s *Server) updateReportTemplate(w http.ResponseWriter, r *http.Request) {
	pg := s.m.PG()
	if pg == nil {
		writeErr(w, 503, "需要 PostgreSQL")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "非法的模板 id")
		return
	}
	var tpl db.ReportTemplate
	if err := json.NewDecoder(r.Body).Decode(&tpl); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if strings.TrimSpace(tpl.Name) == "" {
		writeErr(w, 400, "name 必填")
		return
	}
	tpl.ID = id
	if err := pg.ReportTemplates().Update(r.Context(), tpl); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) deleteReportTemplate(w http.ResponseWriter, r *http.Request) {
	pg := s.m.PG()
	if pg == nil {
		writeErr(w, 503, "需要 PostgreSQL")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "非法的模板 id")
		return
	}
	if err := pg.ReportTemplates().Delete(r.Context(), id); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": 1})
}

func (s *Server) setDefaultReportTemplate(w http.ResponseWriter, r *http.Request) {
	pg := s.m.PG()
	if pg == nil {
		writeErr(w, 503, "需要 PostgreSQL")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "非法的模板 id")
		return
	}
	if err := pg.ReportTemplates().SetDefault(r.Context(), id); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
