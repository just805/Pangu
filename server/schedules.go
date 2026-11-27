package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/just805/pangu/db"
)

// ═══ M-06：周期任务调度 ═══
//
// 边界（硬约束）：只调度「新建侦察/攻击面类任务」，**永不触发复测**——平台已决策
// 「复测纯手动，禁止自动复测/提醒」。调度器只调 launchTask，不碰 finding_retests。
//
// 触发路径统一走 Manager.CreateTaskWithOptions → Server.launchTask → admitTask，
// 因此天然继承并发上限、FIFO 排队与第 0 轮目标拆解，不绕过任何准入逻辑。
const (
	scheduleTickInterval = 30 * time.Second
	scheduleDueBatch     = 10
)

// startScheduleLoop 启动周期任务调度循环。与漏洞情报同步同一取向：独立 goroutine，
// 随 server ctx 退出。
func (s *Server) startScheduleLoop() {
	go func() {
		t := time.NewTicker(scheduleTickInterval)
		defer t.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-t.C:
				s.runDueSchedules(s.ctx)
			}
		}
	}()
}

func (s *Server) runDueSchedules(ctx context.Context) {
	pg := s.m.PG()
	if pg == nil {
		return
	}
	store := pg.TaskSchedules()
	due, err := store.Due(ctx, scheduleDueBatch)
	if err != nil {
		log.Printf("[schedule] 查询到期计划失败: %v", err)
		return
	}
	for _, sc := range due {
		next := time.Now().Add(time.Duration(sc.IntervalMinutes) * time.Minute)
		// 上一轮任务未到终态 → 不叠加执行，顺延一个周期。同一计划堆叠多个任务
		// 既浪费预算，也会让两个任务同时打同一个目标。
		if sc.LastTaskID > 0 {
			if scheduleShouldDefer(s.scheduleLastTaskStatus(sc.LastTaskID)) {
				if err := store.Touch(ctx, sc.ID, next); err != nil {
					log.Printf("[schedule] 计划 #%d 顺延失败: %v", sc.ID, err)
				}
				continue
			}
		}
		s.triggerSchedule(ctx, store, sc, next)
	}
}

// scheduleLastTaskStatus 取上一轮任务的状态：优先内存（最新），回落数据库。
//
// 只查内存是错的：进程重启后 m.tasks 是空的，DB 里那个任务可能还在跑，返回空串
// 会被 scheduleShouldDefer 判成「已结束」而立刻再起一个任务——正好造成本模块
// 最该避免的叠加执行。
func (s *Server) scheduleLastTaskStatus(id int64) string {
	if st := s.m.TaskStatus(strconv.FormatInt(id, 10)); st != "" {
		return st
	}
	if task, err := s.m.PG().GetTask(id); err == nil && task != nil {
		return task.Status
	}
	return ""
}

// scheduleShouldDefer 判断本轮是否放弃触发：上一轮任务还没到终态就顺延一个周期。
// 同一计划堆叠多个任务既浪费预算，也会让两个任务同时打同一个目标。
//
// 空状态（任务已被删/查不到）不阻塞——否则一条历史脏 LastTaskID 会让计划永久停摆。
// 这个判定被单测钉住，改它必须同时改测试。
func scheduleShouldDefer(lastStatus string) bool {
	return lastStatus != "" && !isTerminalStatus(lastStatus)
}

// triggerSchedule 用计划参数新建一个任务并启动。失败只记在计划的 last_error 上，
// 不重试（下一周期自然再试）。
func (s *Server) triggerSchedule(ctx context.Context, store *db.TaskScheduleStore, sc db.TaskSchedule, next time.Time) {
	cov := sc.CoverageEnabled
	task, err := s.m.CreateTaskWithOptions(sc.Description, sc.Goal, db.TaskCreateOptions{
		Name:                 sc.Name,
		CompanyIDs:           sc.CompanyIDs,
		TimeoutSeconds:       sc.TimeoutSeconds,
		PlanHeartbeatSeconds: sc.PlanHeartbeatSeconds,
		CoverageEnabled:      &cov,
	})
	if err != nil {
		log.Printf("[schedule] 计划 #%d 创建任务失败: %v", sc.ID, err)
		_ = store.MarkRun(ctx, sc.ID, 0, next, err.Error())
		return
	}
	s.launchTask(task, "", false)
	var tid int64
	if v, e := strconv.ParseInt(task.ID, 10, 64); e == nil {
		tid = v
	}
	_ = store.MarkRun(ctx, sc.ID, tid, next, "")
	log.Printf("[schedule] 计划 #%d 已触发任务 %s", sc.ID, task.ID)
}

// ── HTTP ──

func (s *Server) listSchedules(w http.ResponseWriter, r *http.Request) {
	pg := s.m.PG()
	if pg == nil {
		writeJSON(w, 200, []any{})
		return
	}
	list, err := pg.TaskSchedules().List(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, list)
}

func (s *Server) createSchedule(w http.ResponseWriter, r *http.Request) {
	pg := s.m.PG()
	if pg == nil {
		writeErr(w, 503, "需要 PostgreSQL")
		return
	}
	var sc db.TaskSchedule
	if err := json.NewDecoder(r.Body).Decode(&sc); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if strings.TrimSpace(sc.Name) == "" {
		writeErr(w, 400, "name 必填（计划名称）")
		return
	}
	if strings.TrimSpace(sc.Goal) == "" {
		writeErr(w, 400, "goal 必填（任务目标）")
		return
	}
	if strings.TrimSpace(sc.Description) == "" {
		sc.Description = sc.Name
	}
	sc.ID = 0
	id, err := pg.TaskSchedules().Create(r.Context(), sc)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"id": id})
}

func (s *Server) updateSchedule(w http.ResponseWriter, r *http.Request) {
	pg := s.m.PG()
	if pg == nil {
		writeErr(w, 503, "需要 PostgreSQL")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "非法的计划 id")
		return
	}
	var sc db.TaskSchedule
	if err := json.NewDecoder(r.Body).Decode(&sc); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if strings.TrimSpace(sc.Name) == "" || strings.TrimSpace(sc.Goal) == "" {
		writeErr(w, 400, "name 与 goal 必填")
		return
	}
	if strings.TrimSpace(sc.Description) == "" {
		sc.Description = sc.Name
	}
	sc.ID = id
	if err := pg.TaskSchedules().Update(r.Context(), sc); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	pg := s.m.PG()
	if pg == nil {
		writeErr(w, 503, "需要 PostgreSQL")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "非法的计划 id")
		return
	}
	if err := pg.TaskSchedules().Delete(r.Context(), id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": 1})
}

// runScheduleNow 立即触发一次（仍然是新建任务，不是复测）。用于验证计划配置是否正确。
func (s *Server) runScheduleNow(w http.ResponseWriter, r *http.Request) {
	pg := s.m.PG()
	if pg == nil {
		writeErr(w, 503, "需要 PostgreSQL")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "非法的计划 id")
		return
	}
	store := pg.TaskSchedules()
	sc, err := store.Get(r.Context(), id)
	if err != nil || sc == nil {
		writeErr(w, 404, "计划不存在")
		return
	}
	next := time.Now().Add(time.Duration(sc.IntervalMinutes) * time.Minute)
	s.triggerSchedule(r.Context(), store, *sc, next)
	writeJSON(w, 200, map[string]any{"ok": true})
}
