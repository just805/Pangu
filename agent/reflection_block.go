package agent

// 终态经验（reflection）的精简投影：planner 态势与 worker 启动消息共用。

import (
	"strconv"
	"strings"

	"github.com/just805/pangu/db"
)

// compactLessons 把终态经验压成紧凑结构（只保留决策用得上的字段）。
func compactLessons(rs []db.IntentReflection) []map[string]any {
	out := make([]map[string]any, 0, len(rs))
	for _, r := range rs {
		m := map[string]any{"intent_id": r.IntentID, "state": r.TerminalState, "yields": r.Yields}
		if r.Dimension != "" {
			m["dimension"] = r.Dimension
		}
		if r.Summary != "" {
			m["summary"] = r.Summary
		}
		if r.StopReason != "" {
			m["stop_reason"] = r.StopReason
		}
		out = append(out, m)
	}
	return out
}

// ReflectionBlock 返回 worker 启动时注入的同维度历史经验（纯文本，空串=不注入）。
// 只给「最近同类方向怎么收场的」，明确标注仅供参考、以本任务实测为准。
func ReflectionBlock(rs []db.IntentReflection) string {
	if len(rs) == 0 {
		return ""
	}
	lines := make([]string, 0, len(rs)+1)
	lines = append(lines, "【本任务同类方向的历史收场（仅供参考，以本任务实测为准）】")
	for _, r := range rs {
		state := r.TerminalState
		switch state {
		case "done":
			state = "完成"
		case "exhausted":
			state = "手段穷尽"
		case "blocked":
			state = "受阻"
		case "stopped":
			state = "被停止"
		}
		line := "- #" + strconv.FormatInt(r.IntentID, 10) + " " + state + "（产出 " + strconv.Itoa(r.Yields) + "）"
		if r.Summary != "" {
			line += "：" + r.Summary
		}
		if r.StopReason != "" {
			line += " ｜原因：" + r.StopReason
		}
		lines = append(lines, line)
	}
	lines = append(lines, "别重复上一条完全相同的打法；若历史是「手段穷尽」，换角度或换入口，而不是原样重跑。")
	return strings.Join(lines, "\n")
}
