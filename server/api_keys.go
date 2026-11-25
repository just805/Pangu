package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ═══ M-12：开放 API 凭证的 HTTP 面 ═══
//
// 创建时**只返回一次明文**；列表只给尾号提示。删除立即失效。
// 权限等同管理员——页面与接口文案都要说清楚，避免用户把它当低权 token 外发。

func (s *Server) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	pg := s.m.PG()
	if pg == nil {
		writeJSON(w, 200, []any{})
		return
	}
	list, err := pg.ListAPIKeys(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, list)
}

func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request) {
	pg := s.m.PG()
	if pg == nil {
		writeErr(w, 503, "需要 PostgreSQL")
		return
	}
	var body struct {
		Name        string `json:"name"`
		ExpiresDays int    `json:"expires_days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeErr(w, 400, "name 必填（用于辨认这把 Key 的用途）")
		return
	}
	var expires *time.Time
	if body.ExpiresDays > 0 {
		t := time.Now().AddDate(0, 0, body.ExpiresDays)
		expires = &t
	}
	key, raw, err := pg.CreateAPIKey(r.Context(), name, expires)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// 明文只在此处返回一次；前端应在关闭弹窗后不再保留。
	writeJSON(w, 200, map[string]any{
		"key":    key,
		"secret": raw,
		"note":   "明文只显示这一次，请立即复制保存。该 Key 权限等同管理员，请勿发到公开渠道。",
	})
}

func (s *Server) deleteAPIKey(w http.ResponseWriter, r *http.Request) {
	pg := s.m.PG()
	if pg == nil {
		writeErr(w, 503, "需要 PostgreSQL")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "非法的 Key id")
		return
	}
	if err := pg.DeleteAPIKey(r.Context(), id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": 1})
}
