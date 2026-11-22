package server

import "net/http"

// taskAttackChain 返回任务攻击链（路线图 C1/C2/C3 的同一数据源）：
// finding 步骤（带证明点）+ 推导出的推进关系边。前端任务详情「攻击链」页
// 与报告「攻击路径」章读同一份结构。
func (s *Server) taskAttackChain(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	chain, err := t.Store.AttackChain()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, chain)
}
