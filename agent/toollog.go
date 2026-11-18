package agent

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"log"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
)

// toolLogHooks 把「谁在什么任务下调用了什么工具、结果如何、怎么收场」写进 journal。
//
// 为什么需要它：worker/planner/main 的工具调用此前**完全不可观测**——journal 里只有
// planner 的心跳和 vulnintel 的刷屏，worker 到底在不在干活、卡在哪一步、有没有报错，
// 都只能去翻 transcripts/*.jsonl 的 mtime 才能确认。调优循环（抓日志→看工具使用→
// 调参数）没有这层信号就等于闭眼调。
//
// 设计取舍：
//   - **不打全量命令/输出**。Bash 命令动辄几百字节、工具输出可达几十 KB，全打会把
//     journal 撑爆并拖慢磁盘。只打【首行摘要 + 字节数】+【结果分类】。
//   - **结果分类而非原文**。ok/error/blocked 三态是调优最需要的信号（判断模型是不是
//     在撞 WAF、是不是工具报参数错），原文去 transcript 看。
//   - **每次调用两行**（pre + post）。pre 行给出"要做什么"，post 行给出"做成了吗"。
//     两行都有才能把"卡在调用前"（模型没输出工具）和"卡在调用后"（工具失败）分开。
//
// 它是纯旁路：不拦截、不改输入、不影响决策。判空返回即可。
type toolLogHooks struct {
	// kind 是 agent 角色标签（planner/worker/main/chat），用于日志前缀。
	kind string
	// label 是更具体的运行标签（如 "worker-1 · #42"），可选。
	label string
	// inner 是已有的 hook 链（拦截管控），本层只做旁路观察后转发。
	inner harness.HookRunner
	// timing 做 Pre/Post 的开始时刻配对（P1 延迟画像）；nil（旧构造路径）时跳过。
	timing *toolTimingStarts
}

// ToolTimingSink 是工具调用延迟的落库出口（P1 延迟画像）。ri 携带
// task/exploration/intent 归属；latencyMS 由 Pre/Post 配对得出；isErr 是工具
// 侧失败标记。由 server 启动时经 SetToolTimingSink 装配（写 db.tool_usage）；
// 未装配时零开销跳过。
type ToolTimingSink func(ri RunInfo, tool string, latencyMS int64, isErr bool)

var toolTimingSink atomic.Pointer[ToolTimingSink]

// SetToolTimingSink 装配全局延迟画像出口（幂等，启动时一次）。
func SetToolTimingSink(fn ToolTimingSink) {
	if fn == nil {
		toolTimingSink.Store(nil)
		return
	}
	toolTimingSink.Store(&fn)
}

func emitToolTiming(ri RunInfo, tool string, latencyMS int64, isErr bool) {
	if fn := toolTimingSink.Load(); fn != nil && *fn != nil {
		(*fn)(ri, tool, latencyMS, isErr)
	}
}

// toolTimingStarts 按「input hash → 待配对开始时刻 FIFO」配对 Pre/Post：
// 一次 assistant 回合并发多个工具调用是常态，同工具名会交错；同参数重试也
// 会同 hash——FIFO 队列（先到先配对）在交错下依然正确。配对失败（Post 无
// 对应 Pre）直接丢样本：画像允许丢样，绝不为主流程添堵。
type toolTimingStarts struct {
	mu     sync.Mutex
	byHash map[uint64][]time.Time
}

func (t *toolTimingStarts) push(hash uint64, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.byHash == nil {
		t.byHash = map[uint64][]time.Time{}
	}
	t.byHash[hash] = append(t.byHash[hash], at)
}

func (t *toolTimingStarts) pop(hash uint64) (time.Time, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	q := t.byHash[hash]
	if len(q) == 0 {
		return time.Time{}, false
	}
	at := q[0]
	t.byHash[hash] = q[1:]
	return at, true
}

func toolInputHash(input []byte) uint64 {
	h := fnv.New64a()
	h.Write(input)
	return h.Sum64()
}

// toolLogSink 构造一个只观察不干预的 hook 链（无 inner）。
func toolLogSink(kind string) *toolLogHooks {
	return &toolLogHooks{kind: kind, timing: &toolTimingStarts{}}
}

// wrapToolLog 把日志层套在已有 hook 链外层：先记日志，再转发给 inner。
// inner 为 nil 时本层就是全部。
func wrapToolLog(inner harness.HookRunner, kind string, label string) *toolLogHooks {
	return &toolLogHooks{kind: kind, label: label, inner: inner, timing: &toolTimingStarts{}}
}

// PreToolUse 记录一次工具调用的开始。
//
// 顺序很重要：**先转发给 inner，再记日志**。因为 inner（拦截/steer）会返回
// 「这次调用被拦了」，日志层需要知道这个结果才能准确标注——
//
//	先记后转发 → 日志只说「要调这个工具」，实际有没有被拦是猜的；
//	先转发后记 → 日志直接标 BLOCKED。
//
// 而「被拦」的判据必须是【平台自己的拦截决定】，不是输出里出现 403/denied
// 这类字样——实测生成漏洞报告的命令（输出里含漏洞描述）被误判成 blocked。
func (h *toolLogHooks) PreToolUse(ctx context.Context, name string, input []byte) (bool, string, []byte) {
	var (
		block   bool
		message string
		updated []byte
	)
	if h.inner != nil {
		block, message, updated = h.inner.PreToolUse(ctx, name, input)
	} else {
		updated = input
	}
	// 延迟画像：以 inner 处理后的 input（模型实际执行的形态）为配对键。
	if h.timing != nil && !block {
		h.timing.push(toolInputHash(updated), time.Now())
	}
	tag := ""
	if block {
		tag = " BLOCKED"
	}
	log.Printf("%s → %s%s %s", h.prefix(ctx), name, tag, summarizeToolInput(name, input))
	return block, message, updated
}

// PostToolUse 记录一次工具调用的结果。
func (h *toolLogHooks) PostToolUse(ctx context.Context, name string, input, result []byte, isErr bool) {
	h.logPost(ctx, name, input, result, isErr)
	// 延迟画像：配对 Pre 记下的开始时刻。被拦调用在 Pre 侧没 push，这里 pop
	// 不到就丢样（正确的口径——拦截没有执行延迟可言）。
	if h.timing != nil {
		if at, ok := h.timing.pop(toolInputHash(input)); ok {
			emitToolTiming(RunInfoFrom(ctx), name, time.Since(at).Milliseconds(), isErr)
		}
	}
	if h.inner != nil {
		h.inner.PostToolUse(ctx, name, input, result, isErr)
	}
}

// Stop 记录一次 run 的收场。
func (h *toolLogHooks) Stop(ctx context.Context, messages []llm.Message) (bool, []string, string) {
	h.logStop(ctx, messages)
	if h.inner != nil {
		return h.inner.Stop(ctx, messages)
	}
	return false, nil, ""
}

// --- 日志行 ---

func (h *toolLogHooks) prefix(ctx context.Context) string {
	ri := RunInfoFrom(ctx)
	kind := ri.Kind
	if kind == "" {
		kind = h.kind
	}
	p := "[tool] " + kind
	if h.label != "" {
		p += " " + h.label
	}
	switch {
	case ri.TaskID != 0:
		p += " task=" + strconv.FormatInt(ri.TaskID, 10)
	case ri.SessionID != "":
		p += " session=" + shortID(ri.SessionID)
	}
	if ri.ExplorationID != 0 {
		p += " exp=" + strconv.FormatInt(ri.ExplorationID, 10)
	}
	if ri.IntentID != 0 {
		p += " intent=#" + strconv.FormatInt(ri.IntentID, 10)
	}
	return p
}

func (h *toolLogHooks) logPost(ctx context.Context, name string, input, result []byte, isErr bool) {
	log.Printf("%s ← %s %s out=%dB", h.prefix(ctx), name, classifyToolResult(name, result, isErr), len(result))
}
// logStop 打收场行：最后一个助手回合有没有正文/工具调用。这是区分
// 「正常做完」「空转收场」「模型报错」的第一手信号。
func (h *toolLogHooks) logStop(ctx context.Context, messages []llm.Message) {
	var textLen int
	var tools int
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Role != llm.RoleAssistant {
			continue
		}
		textLen = len(strings.TrimSpace(m.Text()))
		tools = len(m.ToolUses())
		break
	}
	log.Printf("%s ⏹ stop text=%dB tools=%d msgs=%d", h.prefix(ctx), textLen, tools, len(messages))
}

// --- 工具输入/结果的摘要 ---

// summarizeToolInput 给出工具入参的一行摘要。命令只取第一行（去掉前导空白）并限长，
// 避免把整条多行脚本塞进 journal。
func summarizeToolInput(name string, input []byte) string {
	if len(input) == 0 {
		return ""
	}
	switch name {
	case "Bash":
		var in struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(input, &in) == nil && in.Command != "" {
			return cmdHead(in.Command)
		}
	case "record_fact", "report_finding", "insert_assets", "record_asset":
		var in struct {
			Type  string `json:"type"`
			Title string `json:"title"`
			Kind  string `json:"kind"`
			Name  string `json:"name"`
		}
		if json.Unmarshal(input, &in) == nil {
			var bits []string
			for _, s := range []string{in.Type, in.Kind, in.Name, in.Title} {
				if s != "" {
					bits = append(bits, trunc(s, 48))
				}
			}
			if len(bits) > 0 {
				return strings.Join(bits, " ")
			}
		}
	}
	// 兜底：整体 JSON 压成一行并限长。空对象/空数组没有信息量，返回空串而不是 "{}"。
	s := strings.Join(strings.Fields(string(input)), " ")
	if s == "{}" || s == "[]" {
		return ""
	}
	return trunc(s, 96)
}

// cmdHead 取命令的第一行，限长 96 字符。
func cmdHead(cmd string) string {
	line := cmd
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = line[:i]
	}
	return trunc(strings.TrimSpace(line), 96)
}

// 活动日志/工具摘要保留命令行原文（v0.4.37：第一方证据需可溯源，不再脱敏）。

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func shortID(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}

// reToolBlocked 识别「目标侧拦截」的典型措辞（WAF/403/验证码/限流）。与 guard 的
// 归因正则同源，但这里只用于打标签，不参与决策。
var reToolBlocked = regexp.MustCompile(`(?i)\b(403|forbidden|waf|blocked|rate.?limit|429|captcha|denied|too many requests)\b`)

// platformBlockMarker 是平台拦截消息的固定前缀（见 guard.systemBlockMessage）。
// 出现它 = 这次调用被 PANGU 自己拦了，与目标侧的 WAF 是两回事。
const platformBlockMarker = "【PANGU 平台管控·非目标防御】"

// classifyToolResult 把工具结果压成一个分类标签。
//
// 语义分层，别混：
//   - blocked：平台**自己**拦了这次调用。判据是 PANGU 的固定前缀，精确可靠。
//     （PreToolUse 行会标 BLOCKED，这里是兜底——理论上被拦的调用不会有 post 行。）
//   - waf：输出里出现 WAF/403/denied 措辞，**只对 Bash 生效**。Bash 是打目标的，
//     命令输出里的 403 说明被目标挡了——这是给 planner 做归因的有用信号。
//     但数据类工具（list_findings/list_assets）的输出里出现 403/denied 往往只是
//     **漏洞描述本身**，实测 30KB 的漏洞列表和一份漏洞报告都被误判过。
//   - error：工具执行报错。
//   - ok：其余。
//
// 为什么不把 waf 折进 blocked：实测「生成漏洞报告」的命令（输出含漏洞描述）
// 被判成 blocked，而「被平台拦截」是完全不同的语义——前者是正常干活，后者是
// 被平台管控。混在一起会让 blocked 的计数完全失去意义。
func classifyToolResult(name string, result []byte, isErr bool) string {
	s := string(result)
	if isErr {
		return "error"
	}
	if strings.Contains(s, platformBlockMarker) {
		return "blocked"
	}
	if name == "Bash" && reToolBlocked.MatchString(s) {
		return "waf"
	}
	return "ok"
}
