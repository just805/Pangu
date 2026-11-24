package server

import (
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/just805/pangu/agent"
	"github.com/just805/pangu/db"
)

// 任务级预算网关（上游 0.4.101）。
//
// 与意图级软预算（engine_guard.go，单条意图的 rust 上限）互补：这里是「整个任务」
// 的累计 token 开销闸门。到软阈值（默认 80%）提醒 planner 收敛——停止铺新面、
// 优先把已有结论写回并判目标；到硬阈值（100%）走统一收尾止损。软提醒用设置键
// 做 30 分钟节流，避免每轮规划都刷。
//
// 只做 token 维度：本地无计费价格表（USD 会失真），分钟维度已由任务 deadline /
// autopilot 延寿机制覆盖，不重复实现。

const (
	settingTaskBudgetTokens  = "task_budget_tokens"   // 0=关闭
	settingTaskBudgetWarnPct = "task_budget_warn_pct" // 默认 80
	defaultTaskBudgetWarnPct = 80
	taskBudgetWarnThrottle   = 30 * time.Minute
)

type taskBudgetConfig struct {
	Enabled bool
	Tokens  int64
	WarnPct int
}

// taskBudgetFromPG 读取全局设置并夹逼。读库失败回默认（关闭），绝不把预算变成硬故障。
func taskBudgetFromPG(pg *db.DB) taskBudgetConfig {
	c := taskBudgetConfig{Enabled: false, WarnPct: defaultTaskBudgetWarnPct}
	if pg == nil {
		return c
	}
	if n, ok := settingInt(pg, settingTaskBudgetTokens); ok && n > 0 {
		c.Enabled = true
		c.Tokens = int64(clampInt(n, 1_000_000, 500_000_000))
	}
	if n, ok := settingInt(pg, settingTaskBudgetWarnPct); ok {
		c.WarnPct = clampInt(n, 50, 95)
	}
	return c
}

// evaluateTaskBudget 是纯判定函数（便于单测）：返回是否进入软提醒窗口 / 是否到硬上限。
// 关闭或 tokens<=0 时都返回 false。
func evaluateTaskBudget(total int64, cfg taskBudgetConfig) (warn, hard bool) {
	if !cfg.Enabled || cfg.Tokens <= 0 {
		return false, false
	}
	if total >= cfg.Tokens {
		return true, true
	}
	warnAt := cfg.Tokens * int64(cfg.WarnPct) / 100
	if warnAt <= 0 {
		warnAt = cfg.Tokens
	}
	return total >= warnAt, false
}

// checkTaskBudget 在规划轮入口检查任务累计 token。
// 返回 true 表示已到硬上限、已登记收尾（调用方应直接结束本轮，省掉一次 LLM 调用）；
// 返回 false 时若进入软提醒窗口，会向本轮 pendingTriggers 追加一条 budget 事件
// （由紧接着的 drainTriggers 取走，随本轮规划一起下发给 planner）。
func (e *Engine) checkTaskBudget(t *Task) bool {
	if e == nil || e.m == nil || e.m.pg == nil || t == nil || t.Store == nil {
		return false
	}
	cfg := taskBudgetFromPG(e.m.pg)
	if !cfg.Enabled || cfg.Tokens <= 0 {
		return false
	}
	u, err := t.Store.TokenTotal()
	if err != nil {
		return false
	}
	total := int64(u.InputTokens) + int64(u.OutputTokens)
	warn, hard := evaluateTaskBudget(total, cfg)
	if hard {
		detail := fmt.Sprintf("任务累计 token 已达预算上限（%d/%d）", total, cfg.Tokens)
		t.requestSettle("auto_budget", detail)
		log.Printf("[budget task] task %s %s，登记收尾", t.ID, detail)
		return true
	}
	if !warn {
		return false
	}
	key := "task_budget_warn_" + t.ID
	if v, ok, _ := e.m.pg.GetSetting(key); ok {
		if ts, err := strconv.ParseInt(v, 10, 64); err == nil && time.Since(time.Unix(ts, 0)) < taskBudgetWarnThrottle {
			return false
		}
	}
	_ = e.m.pg.SetSetting(key, strconv.FormatInt(time.Now().Unix(), 10))
	pct := total * 100 / cfg.Tokens
	msg := fmt.Sprintf("任务 token 预算提醒：本任务累计已用约 %d / %d（约 %d%%）。停止铺新面：本轮最多派 1 条收尾意图，"+
		"把已有结论用 record_fact（发现漏洞则 report_finding）写回；并优先判定目标是否已可达成。", total, cfg.Tokens, pct)
	t.trigMu.Lock()
	t.pendingTriggers = append(t.pendingTriggers, agent.TriggerEvent{Kind: "budget", Detail: msg})
	t.trigMu.Unlock()
	log.Printf("[budget task] task %s 达软阈值（%d/%d，%d%%），已注入收敛提醒", t.ID, total, cfg.Tokens, pct)
	return false
}
