package agent

import (
	"fmt"
	"sort"
	"strings"

	"github.com/just805/pangu/db"
)

// 高错误率工具标注（上游 0.4.114/115）。
//
// worker 每轮拿到本任务里「反复失败的工具」Top3，启动前就知道哪些手段在当前
// 目标上不稳，先换参数/换等价工具，而不是原样头铁重跑。数据来自已有的工具延迟
// 画像账本（db.ToolLatencyAggregate），纯旁路、不改调用主流程。

// toolPerfMinCalls / toolPerfErrorRate：样本太稀疏（<5 次）不发提示——偶发一次
// 失败不代表工具不可用；错误率门槛 60% 以上才算「反复失败」。
const (
	toolPerfMinCalls  = 5
	toolPerfErrorRate = 0.60
	toolPerfTopN      = 3
)

// ToolPerfHint 是一个「反复失败」工具的聚合结果。
type ToolPerfHint struct {
	Tool      string
	Calls     int64
	Errors    int64
	ErrorRate float64
}

// BuildToolPerfHints 从工具延迟聚合里筛出 worker 侧的高错误率工具，按错误率
// 降序取 Top N。纯函数，便于单测；统计为空/稀疏时返回 nil。
func BuildToolPerfHints(stats []db.ToolLatencyStat) []ToolPerfHint {
	out := make([]ToolPerfHint, 0, len(stats))
	for _, s := range stats {
		if s.Role != "worker" || s.Calls < toolPerfMinCalls || s.Calls <= 0 {
			continue
		}
		rate := float64(s.Errors) / float64(s.Calls)
		if rate < toolPerfErrorRate {
			continue
		}
		out = append(out, ToolPerfHint{Tool: s.Tool, Calls: s.Calls, Errors: s.Errors, ErrorRate: rate})
	}
	if len(out) == 0 {
		return nil
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ErrorRate != out[j].ErrorRate {
			return out[i].ErrorRate > out[j].ErrorRate
		}
		return out[i].Calls > out[j].Calls
	})
	if len(out) > toolPerfTopN {
		out = out[:toolPerfTopN]
	}
	return out
}

// ToolPerfBlock 渲染注入 worker 系统提示的文本块；空切片返回 ""（不注入）。
func ToolPerfBlock(hints []ToolPerfHint) string {
	if len(hints) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n【本任务工具表现（确定性统计，仅供参考）】这些工具近期反复失败：")
	for _, h := range hints {
		b.WriteString(fmt.Sprintf("\n- %s：%d 次中 %d 次报错（错误率 %.0f%%）",
			h.Tool, h.Calls, h.Errors, h.ErrorRate*100))
	}
	b.WriteString("\n失败优先查目标可达性/参数是否正确；同一调用别原样重试，换等价工具或换打法。")
	return b.String()
}
