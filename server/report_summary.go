// A13 执行摘要：LLM 从 findings 聚合生成管理层风险结论，存库（settings kv）
// 可人工修订后随报告导出（md 与 docx 同源渲染）。
//
// 端点：
//   GET  /api/report/summary?task=   读当前摘要（无则 {summary:"",exists:false}）
//   PUT  /api/report/summary?task=   人工保存 {"summary":"..."}
//   POST /api/report/summary?task=   LLM 生成并保存（幂等重生成，覆盖旧值）
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/Autumn-27/norma/llm"
	"github.com/just805/pangu/db"
	"github.com/just805/pangu/report"
)

// execSummaryKey 摘要存 settings kv（任务维度唯一键）。settings 是全局 kv，
// 键带任务 id 天然隔离；删除任务不清理（无害残留，量级 ~1KB/任务）。
func execSummaryKey(taskID string) string { return "exec_summary_task_" + taskID }

const execSummaryMaxLen = 8 << 10 // 8KB 上限：摘要不该是长文，防御异常输入

func (s *Server) reportSummaryGet(w http.ResponseWriter, r *http.Request) {
	t := s.m.ResolveTask(r.URL.Query().Get("task"))
	if t == nil {
		writeErr(w, 404, "no active task")
		return
	}
	v, _, _ := s.m.pg.GetSetting(execSummaryKey(t.ID))
	writeJSON(w, 200, map[string]any{"summary": v, "exists": strings.TrimSpace(v) != ""})
}

func (s *Server) reportSummaryPut(w http.ResponseWriter, r *http.Request) {
	t := s.m.ResolveTask(r.URL.Query().Get("task"))
	if t == nil {
		writeErr(w, 404, "no active task")
		return
	}
	var req struct {
		Summary string `json:"summary"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	summary := strings.TrimSpace(req.Summary)
	if len(summary) > execSummaryMaxLen {
		writeErr(w, 400, fmt.Sprintf("摘要超长（>%d 字节），请精简", execSummaryMaxLen))
		return
	}
	if err := s.m.pg.SetSetting(execSummaryKey(t.ID), summary); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"summary": summary, "saved": true})
}

func (s *Server) reportSummaryGenerate(w http.ResponseWriter, r *http.Request) {
	t := s.m.ResolveTask(r.URL.Query().Get("task"))
	if t == nil {
		writeErr(w, 404, "no active task")
		return
	}
	summary, err := s.generateExecSummary(r.Context(), t)
	if err != nil {
		writeErr(w, 502, "生成执行摘要失败: "+err.Error())
		return
	}
	if err := s.m.pg.SetSetting(execSummaryKey(t.ID), summary); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"summary": summary, "generated": true})
}

// execSummarySevRank 严重度排序键（与 report 包口径一致）。
func execSummarySevRank(f *db.DBFinding) int {
	switch strings.ToLower(f.Severity) {
	case "critical":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	case "low":
		return 3
	default:
		return 4
	}
}

// reportFindingTitle server 侧的漏洞标题（与 report.findingTitle 口径一致）。
func reportFindingTitle(f *db.DBFinding) string {
	if strings.TrimSpace(f.Name) != "" {
		return f.Name
	}
	return strings.TrimSpace(f.VulnClass)
}

// reportAssetList server 侧资产名拼接（AssetNames 已由共享组装解析好）。
func reportAssetList(ids []int64, names map[int64]string) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		if n, ok := names[id]; ok && strings.TrimSpace(n) != "" {
			parts = append(parts, n)
			continue
		}
		parts = append(parts, fmt.Sprintf("id:%d", id))
	}
	return strings.Join(parts, ", ")
}

// execSummaryDigest 组装给 LLM 的输入：严重度统计 + 按严重度取 top 12 条
// （名称/资产/摘要截断），不让模型读全量正文（成本与跑题双向控制）。
func (s *Server) execSummaryDigest(in report.TaskDocxInput) string {
	counts := map[string]int{}
	for _, f := range in.Findings {
		counts[strings.ToLower(strings.TrimSpace(f.Severity))]++
	}
	var b strings.Builder
	fmt.Fprintf(&b, "任务：%s\n", in.Title)
	if in.Goal != "" {
		fmt.Fprintf(&b, "目标：%s\n", in.Goal)
	}
	order := []string{"critical", "high", "medium", "low", "info"}
	parts := make([]string, 0, len(order))
	for _, sev := range order {
		if n := counts[sev]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", report.SeverityCN(sev), n))
		}
	}
	b.WriteString("严重度分布：" + strings.Join(parts, "、") + "\n\n主要发现：\n")
	ranked := append([]*db.DBFinding(nil), in.Findings...)
	sort.SliceStable(ranked, func(i, j int) bool { return execSummarySevRank(ranked[i]) < execSummarySevRank(ranked[j]) })
	for i, f := range ranked {
		if i >= 12 {
			break
		}
		assets := reportAssetList(f.AssetIDs, in.AssetNames)
		if assets == "" {
			assets = "—"
		}
		fmt.Fprintf(&b, "%d. [%s] %s · 资产: %s · %s\n",
			i+1, strings.ToUpper(f.Severity), reportFindingTitle(f), assets, truncateRunes(f.Summary, 120))
	}
	return b.String()
}

// generateExecSummary 调任务 LLM 链（planner 通道，与压缩器同款 runtime）生成
// 摘要文本。temperature 0 保证可复现；输出为纯文本，不落 markdown 标题。
func (s *Server) generateExecSummary(ctx context.Context, t *Task) (string, error) {
	in, err := s.assembleTaskReport(ctx, t)
	if err != nil {
		return "", err
	}
	if len(in.Findings) == 0 {
		return "", fmt.Errorf("任务暂无确认漏洞，无法生成执行摘要")
	}
	sys := "你是渗透测试交付报告的执行摘要撰写器。输入是本次授权测试任务的漏洞统计与主要发现清单。" +
		"请面向客户管理层输出一份不超过 350 字的中文执行摘要：\n" +
		"① 一句总体风险态势评价；\n" +
		"② 点名最需要管理层关注的风险（按危害取舍，不逐条罗列），说明业务影响；\n" +
		"③ 一句处置优先级建议。\n" +
		"要求：客观克制、不夸大不淡化；不出现平台内部术语；纯文本输出，不用 markdown 标题，可用短句分点。"
	temp := 0.0
	rt := &taskLLMRuntime{s: s, taskID: t.ID, agentKey: "planner"}
	msg, _, _, err := rt.Complete(ctx, llm.CompletionRequest{
		System:      []string{sys},
		Messages:    []llm.Message{llm.UserText(s.execSummaryDigest(in))},
		MaxTokens:   1200,
		Temperature: &temp,
	})
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(msg.Text())
	if text == "" {
		return "", fmt.Errorf("模型返回空摘要")
	}
	if len(text) > execSummaryMaxLen {
		text = text[:execSummaryMaxLen]
	}
	return text, nil
}
