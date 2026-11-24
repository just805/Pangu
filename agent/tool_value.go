package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/just805/pangu/db"
)

// 价值评分驱动的态势截断（上游 0.4.105/106 的最小落地）。
//
// 本地已有完整的态势体积治理（家族文本预算、cold_digests、按需 node_detail）与
// 回归看门狗（autopilot 停滞唤醒 / auto_finish 连续零新增收尾），无需重复建设。
// 这里只补一个缺口：概览里的 recent_facts / recent_done_intents 过去是「纯按最新
// 截断」，会把早期高价值结论（observed + 有证据、被成功意图产出）挤掉。价值评分让
// 截断先保高价值、再按最新补足，规模未超上限时行为与旧版一致。

// factValueScore 给一条 fact 打分（越高越值得留在概览窗口里）。
//
// 依据全部来自已有 payload / 血缘，不引入新查询：
//   - confidence=observed（直接看到）> inferred（推断）> 缺失；
//   - 有 evidence（一行关键证据）加分；
//   - 由有下游产出的意图产出的加分；由烧了很多 token 却零下游的意图产出的减分。
func factValueScore(n *db.Node, fromIntent int64, yieldsOf map[int64][]int64, tokens map[int64]int64) int {
	if n == nil {
		return 0
	}
	score := 1
	var p map[string]any
	if json.Unmarshal(n.Payload, &p) == nil {
		switch strings.ToLower(strings.TrimSpace(fmt.Sprint(p["confidence"]))) {
		case "observed", "confirmed":
			score += 5
		case "inferred":
			score += 1
		}
		if ev, ok := p["evidence"]; ok && ev != nil && strings.TrimSpace(fmt.Sprint(ev)) != "" {
			score += 3
		}
	}
	if fromIntent > 0 {
		if len(yieldsOf[fromIntent]) > 0 {
			score += 2
		} else if tk, ok := tokens[fromIntent]; ok && tk > 0 {
			score -= 2 // 昂贵却零下游的方向产出的 fact，优先级让位
		}
	}
	return score
}

// selectByScore 返回长度 n 的保留掩码：先按分数降序保底 highValue 条，其余按原顺序
// （调用方传入的列表已是最新在前）补足到 cap 条。n<=cap 时全保留（旧行为不变）。
func selectByScore(n, cap, highValue int, score func(int) int) []bool {
	keep := make([]bool, n)
	if n <= 0 {
		return keep
	}
	if n <= cap || cap <= 0 {
		for i := range keep {
			keep[i] = true
		}
		return keep
	}
	if highValue > cap {
		highValue = cap
	}
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	// 稳定排序：分数相同保持原顺序（新→旧）。
	sortIntsStable(idx, score)
	for i := 0; i < highValue && i < n; i++ {
		keep[idx[i]] = true
	}
	kept := highValue
	for i := 0; i < n && kept < cap; i++ {
		if !keep[i] {
			keep[i] = true
			kept++
		}
	}
	return keep
}

// sortIntsStable 按 score 降序稳定排序 idx（避免引入额外排序依赖的小工具）。
func sortIntsStable(idx []int, score func(int) int) {
	for i := 1; i < len(idx); i++ {
		for j := i; j > 0 && score(idx[j]) > score(idx[j-1]); j-- {
			idx[j], idx[j-1] = idx[j-1], idx[j]
		}
	}
}

// selectFactsByValue 按价值评分选取 fact 窗口，输出仍保持传入顺序（新→旧）。
func selectFactsByValue(facts []*db.Node, cap, highValue int, factFrom map[int64]int64, yieldsOf map[int64][]int64, tokens map[int64]int64) []*db.Node {
	keep := selectByScore(len(facts), cap, highValue, func(i int) int {
		return factValueScore(facts[i], factFrom[facts[i].ID], yieldsOf, tokens)
	})
	out := make([]*db.Node, 0, cap)
	for i, n := range facts {
		if keep[i] {
			out = append(out, n)
		}
	}
	return out
}

// selectIntentsByValue 按「有下游产出优先」选取终态意图窗口，输出仍保持传入顺序。
// 有 yields 的意图（真的产出了事实/发现）优先保留，零产出且高消耗的让位给更新的方向。
func selectIntentsByValue(intents []*db.Node, cap, highValue int, yieldsOf map[int64][]int64, tokens map[int64]int64) []*db.Node {
	keep := selectByScore(len(intents), cap, highValue, func(i int) int {
		n := intents[i]
		score := len(yieldsOf[n.ID]) * 2
		if score == 0 {
			if tk, ok := tokens[n.ID]; ok && tk > 0 {
				score = -1
			}
		}
		return score
	})
	out := make([]*db.Node, 0, cap)
	for i, n := range intents {
		if keep[i] {
			out = append(out, n)
		}
	}
	return out
}
