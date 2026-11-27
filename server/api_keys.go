package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/just805/pangu/db"
)

// ═══ M-12：开放 API 凭证的 HTTP 面 ═══
//
// 创建时**只返回一次明文**；列表只给尾号提示。删除立即失效。
// 权限由 scope 决定（read < write < admin），不再是「一律等同管理员」——程序化
// 凭证外泄时，影响面应当是它能看/能改的范围，而不是整台平台。

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
		Scope       string `json:"scope"`
		ExpiresDays int    `json:"expires_days"`
		// ConfirmAdmin 是建 admin 档的显式确认位：全权 Key 一旦外泄等于交出平台，
		// 靠「记得选对下拉框」防不住，要求调用方多写一个字段。
		ConfirmAdmin bool `json:"confirm_admin"`
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
	// scope 必须显式给：缺省降级会让「忘了选」变成一次静默的权限决策。
	scope := strings.ToLower(strings.TrimSpace(body.Scope))
	switch scope {
	case db.APIKeyScopeRead, db.APIKeyScopeWrite, db.APIKeyScopeAdmin:
	case "":
		writeErr(w, 400, "scope 必填：read（只读）/ write（改业务数据）/ admin（全权）")
		return
	default:
		writeErr(w, 400, "scope 只能是 read / write / admin")
		return
	}
	if scope == db.APIKeyScopeAdmin && !body.ConfirmAdmin {
		writeErr(w, 400, "创建 admin 档 Key 需要 confirm_admin=true（全权凭证，请确认用途）")
		return
	}
	var expires *time.Time
	if body.ExpiresDays > 0 {
		t := time.Now().AddDate(0, 0, body.ExpiresDays)
		expires = &t
	}
	key, raw, err := pg.CreateAPIKey(r.Context(), name, scope, expires)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// 明文只在此处返回一次；前端应在关闭弹窗后不再保留。
	writeJSON(w, 200, map[string]any{
		"key":    key,
		"secret": raw,
		"note":   "明文只显示这一次，请立即复制保存。权限范围：" + scope + "，请按用途最小化授权。",
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
