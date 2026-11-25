package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// ═══ M-07：资产快照与变更检测的 HTTP 面 ═══
//
// 三个动作：采快照、列快照、比较两次快照。
// scope_key 为空=全站；'company:<id>'=单个企业（对应资产页的归属筛选）。

func (s *Server) takeAssetSnapshot(w http.ResponseWriter, r *http.Request) {
	pg := s.m.PG()
	if pg == nil {
		writeErr(w, 503, "需要 PostgreSQL")
		return
	}
	var body struct {
		ScopeKey string `json:"scope_key"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	snap, err := pg.AssetSnapshots().TakeSnapshot(r.Context(), strings.TrimSpace(body.ScopeKey))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, snap)
}

func (s *Server) listAssetSnapshots(w http.ResponseWriter, r *http.Request) {
	pg := s.m.PG()
	if pg == nil {
		writeJSON(w, 200, []any{})
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("scope_key"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := pg.AssetSnapshots().List(r.Context(), scope, limit)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, list)
}

func (s *Server) assetSnapshotDiff(w http.ResponseWriter, r *http.Request) {
	pg := s.m.PG()
	if pg == nil {
		writeErr(w, 503, "需要 PostgreSQL")
		return
	}
	newID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || newID <= 0 {
		writeErr(w, 400, "非法的快照 id")
		return
	}
	baseID, _ := strconv.ParseInt(r.URL.Query().Get("base"), 10, 64)
	diff, err := pg.AssetSnapshots().Diff(r.Context(), baseID, newID)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, diff)
}
