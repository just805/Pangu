package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"sync/atomic"

	"github.com/just805/pangu/agent"
	"github.com/just805/pangu/db"
)

// 意图级软预算 + 重复调用检测（v0.4.28，同行对比分析 P0 #3/#5）。
//
// 来源：hackingBuddyGPT 的四维 Limits（MIT）与 PentAGI 的重复检测器（MIT）——只借鉴
// 机制，全部自研实现。设计取向是「软」：预算到顶不杀进程，而是注入一条 steer 指令
// 要求立即写回收尾；重复调用先拦两次（给模型改参数的机会），再放行兜底，绝不造成
// 比现状更差的卡死路径。任务级 timeout / auto-finish 仍是最终兜底。

const (
	settingIntentBudgetEnabled = "intent_budget_enabled"
	settingIntentBudgetTokens  = "intent_budget_tokens"
	// v0.4.29（P0b）：证据逐字回验档位 off|warn|block（默认 warn）与工具输出
	// 注入告警开关（默认开）。
	settingEvidenceVerify = "finding_evidence_verify"
	settingInjectionGuard = "tool_output_injection_guard"

	defaultIntentBudgetEnabled = true
	defaultIntentBudgetTokens  = 3_000_000
)

// 意图队列阈值（default 8）：本轮新增上限的基准，0=关闭节流。
const (
	settingIntentQueueLimit   = "intent_queue_limit"
	defaultIntentQueueLimit   = 8
)

func intentQueueLimitFromPG(pg *db.DB) int {
	if pg == nil {
		return defaultIntentQueueLimit
	}
	if v, ok := settingInt(pg, settingIntentQueueLimit); ok {
		if v < 0 {
			v = 0
		}
		if v > 200 {
			v = 200
		}
		return v
	}
	return defaultIntentQueueLimit
}

// 串行前置闸门（v0.4.68）：intent_requires_gate=1 时，带 requires_intents 的意图
// 只有在全部前置意图进入终态后才可被领取。默认关闭——开着而规划者又滥用依赖会
// 造成排队停滞，所以只作为严格串行链的显式开关。
const settingIntentRequiresGate = "intent_requires_gate"

func intentRequiresGateFromPG(pg *db.DB) bool {
	if pg == nil {
		return false
	}
	v, ok := settingInt(pg, settingIntentRequiresGate)
	return ok && v == 1
}

// intentQueueBudget 是注入给 agent 的读取器：返回 (生效阈值, 当前全局 worker 数)。
// 生效阈值 = max(设置值, 2*workers)：保证 open 队列至少能缓冲一轮并发完成。
func (s *Server) intentQueueBudget() (int, int) {
	workers := s.m.Workers()
	return effectiveIntentThreshold(intentQueueLimitFromPG(s.m.pg), workers), workers
}

// effectiveIntentThreshold 把设置值折算成实际阈值；0（关闭）保持 0。
func effectiveIntentThreshold(limit, workers int) int {
	if limit <= 0 {
		return 0
	}
	if 2*workers > limit {
		return 2 * workers
	}
	return limit
}

// intentBacklogFull 判断单任务未完成意图是否已达派发上限（心跳轮跳过用：
// 积压满时再规划只会被 add_intent 拒绝、白烧 token）。边沿触发不受影响。
func (e *Engine) intentBacklogFull(t *Task) bool {
	if e == nil || e.m == nil || t == nil || t.Store == nil {
		return false
	}
	limit := effectiveIntentThreshold(intentQueueLimitFromPG(e.m.pg), e.m.Workers())
	if limit <= 0 {
		return false
	}
	active, err := t.Store.CountActiveIntents()
	return err == nil && active >= limit
}

type intentBudgetConfig struct {
	Enabled bool
	Tokens  int64
}

// intentBudgetFromPG 读取全局设置并夹逼。读库失败用默认值——设置查询失败绝不能
// 把意图预算变成硬故障。
func intentBudgetFromPG(pg *db.DB) intentBudgetConfig {
	c := intentBudgetConfig{Enabled: defaultIntentBudgetEnabled, Tokens: defaultIntentBudgetTokens}
	if pg == nil {
		return c
	}
	if v, ok, err := pg.GetSetting(settingIntentBudgetEnabled); err == nil && ok {
		c.Enabled = v == "true" || v == "1"
	}
	if n, ok := settingInt(pg, settingIntentBudgetTokens); ok {
		c.Tokens = int64(clampInt(n, 500_000, 20_000_000))
	}
	return c
}

// budgetNudgeText 是预算到顶后的 steer 指令。要求收尾而不是杀进程：模型手里的结论
// 还没写回时，硬杀等于把这一意图的工作全丢掉。
func budgetNudgeText(limit int64) string {
	return fmt.Sprintf(
		"【预算提醒】本意图的 token 消耗已达到预算上限（约 %d）。立即停止扩大探索范围："+
			"把已经拿到的结论用 record_fact（发现漏洞则 report_finding）写回，然后直接给出结束答复。"+
			"不要再开启新的探测动作；没来得及验证的方向留待规划者另派。",
		limit)
}

// repeatTracker 跟踪一条意图内「同名工具+完全相同入参」的连续调用。
// steerHooks 是值拷贝传递，计数器必须以指针共享。
type repeatTracker struct {
	mu       atomic.Pointer[repeatState]
	blockCap int // 同一串最多拦截次数，超过放行兜底（防活锁）
}

type repeatState struct {
	name    string
	hash    string
	streak  int
	blocked int
}

func newRepeatTracker() *repeatTracker {
	return &repeatTracker{blockCap: 2}
}

// canonicalToolInput 把工具入参压成空白无关的规范形（JSON 紧凑化），失败退原文。
func canonicalToolInput(name string, input []byte) string {
	var compact bytes.Buffer
	if err := json.Compact(&compact, input); err != nil {
		return name + "|" + string(input)
	}
	sum := sha256.Sum256(append([]byte(name+"|"), compact.Bytes()...))
	return hex.EncodeToString(sum[:])
}

// observe 记录一次工具调用，返回 (连续相同次数, 是否应拦截)。
// 规则：同一 (tool, 规范化入参) 连续第 3 次起拦截；同一串最多拦 blockCap 次，
// 之后放行（宁可重复也不活锁）。入参一旦变化立即重置。
func (rt *repeatTracker) observe(name string, input []byte) (int, bool) {
	h := canonicalToolInput(name, input)
	for {
		cur := rt.mu.Load()
		if cur == nil {
			next := &repeatState{name: name, hash: h, streak: 1}
			if rt.mu.CompareAndSwap(nil, next) {
				return 1, false
			}
			continue
		}
		if cur.name != name || cur.hash != h {
			next := &repeatState{name: name, hash: h, streak: 1}
			if rt.mu.CompareAndSwap(cur, next) {
				return 1, false
			}
			continue
		}
		next := &repeatState{name: cur.name, hash: cur.hash, streak: cur.streak + 1, blocked: cur.blocked}
		if rt.mu.CompareAndSwap(cur, next) {
			shouldBlock := cur.streak+1 >= 3 && cur.blocked < rt.blockCap
			if shouldBlock {
				next.blocked++
			}
			return cur.streak + 1, shouldBlock
		}
	}
}

func repeatBlockText(streak int) string {
	return fmt.Sprintf(
		"【重复调用提醒】这个工具调用与你最近的调用完全相同（连续第 %d 次），本次未执行。"+
			"同样的输入只会得到同样的输出——请改变其中一项：换目标/换参数/换方法；"+
			"或者承认当前手段已到头，把结论用 record_fact 写回（否定结论也有效）。",
		streak)
}

// intentBudgetHardMultiplier 是「硬收尾阈值 = 软预算 × 该倍数」。
//
// 软提醒（steer 注入收尾指令）已经给过一次机会；累计到 2× 仍没收场，说明模型
// 在提醒后继续空烧——强杀意图止损（复用 kill_work 的同一引擎通道，终态按
// killed 归因，terminalreason 可见）。2 的由来：写回/落库本身要消耗 token，
// 给一倍余量；再宽硬闸就名存实亡（线上教训：单意图 300 万 input token 的
// 任务 5，软提醒拦不住持续空烧）。
const intentBudgetHardMultiplier = 2

// intentBudgetWatcher 把一条意图的「预算到顶→steer 提醒；2× 到顶→强杀止损」
// 接到 activity 流上。usage 活动的 InputTokens/OutputTokens 是会话累计值
// （agent/capture.go），取最新一条即可判断是否到顶。返回的闭包包在 runIntent
// 的 emit 外层；disabled 时原样返回。kill 为空时不启用硬闸（只软提醒，兼容旧调用方）。
func intentBudgetWatcher(cfg intentBudgetConfig, steer func(string) error, kill func() error, label string) func(db.Activity) {
	if !cfg.Enabled || cfg.Tokens <= 0 {
		return nil
	}
	var nudged, killed atomic.Bool
	hardAt := cfg.Tokens * intentBudgetHardMultiplier
	return func(a db.Activity) {
		if a.Kind != "usage" || a.InputTokens == nil {
			return
		}
		var out int
		if a.OutputTokens != nil {
			out = *a.OutputTokens
		}
		total := int64(*a.InputTokens) + int64(out)
		if total < cfg.Tokens {
			return
		}
		if !nudged.Swap(true) {
			if err := steer(budgetNudgeText(cfg.Tokens)); err != nil {
				log.Printf("[budget %s] 预算提醒入队失败: %v", label, err)
			} else {
				log.Printf("[budget %s] 意图 token 达预算上限 %d（usage 累计 %d），已注入收尾提醒", label, cfg.Tokens, total)
			}
		}
		// 硬闸：软提醒之后累计到 hardAt 仍没收场 → 强杀意图止损（只杀一次）。
		// kill 失败不影响判断（下一条 usage 活动不会再触发——killed 已置位），
		// 任务级 timeout / auto-finish 是最终兜底。
		if total >= hardAt && !killed.Swap(true) {
			log.Printf("[budget %s] usage 累计 %d 已达硬阈值 %d（软预算 %d ×%d）且软提醒后未收场，强杀意图止损", label, total, hardAt, cfg.Tokens, intentBudgetHardMultiplier)
			if err := kill(); err != nil {
				log.Printf("[budget %s] 强杀失败（交由任务级兜底）: %v", label, err)
			}
		}
	}
}

// --- v0.4.29（P0b）：证据回验档位 / 注入告警 -----------------------------------

// evidenceVerifyModeFromPG 读取证据回验档位（off|warn|block，默认 warn）。
// 读库失败回 warn：宁可提示、不可因设置不可用把上报打回。
func evidenceVerifyModeFromPG(pg *db.DB) agent.EvidenceVerifyMode {
	if pg == nil {
		return agent.EvidenceVerifyWarn
	}
	v, ok, err := pg.GetSetting(settingEvidenceVerify)
	if err != nil || !ok {
		return agent.EvidenceVerifyWarn
	}
	return agent.EvidenceVerifyModeFromString(v)
}

// injectionGuardFromPG 读取注入告警开关（默认开）。
func injectionGuardFromPG(pg *db.DB) bool {
	if pg == nil {
		return true
	}
	return pg.GetBool(settingInjectionGuard, true)
}

// injectionPatterns / scanInjection / injectionAlertText 已抽到 agent 包
// （agent/injection.go），与不可信工具输出包裹共用同一份特征。这里保留
// 同名薄包装，避免 engine.go 的调用点跟着改。

// scanInjection 返回命中的特征串（未命中返回空串）。
func scanInjection(text string) string { return agent.ScanInjection(text) }

// injectionAlertText 注入告警文案：经 steer 通道在下一次工具调用前送达模型。
func injectionAlertText(hit string) string { return agent.InjectionAlertText(hit) }
